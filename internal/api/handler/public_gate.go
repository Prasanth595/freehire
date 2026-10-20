package handler

import (
	"context"
	"crypto/subtle"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"

	"github.com/strelov1/freehire/internal/api/ratelimit"
	"github.com/strelov1/freehire/internal/identity/auth"
	"github.com/strelov1/freehire/internal/identity/billing"
	"github.com/strelov1/freehire/internal/platform/db"
)

// The commercial API's budgets (openspec/changes/commercial-api-paid-keys).
//
// Anonymous: measured on prod for 2026-10-19, 34 of 24,859 API-calling IPs made more than
// 2,000 requests that day, and they were catalogue mirrors; the ai-job-search skill's IPs
// average 14 a day but burst to 200–300 a minute. So the DAY cap is what stops a mirror, and
// the minute cap is set where a skill burst degrades into Retry-After rather than breaking.
// Both are shared by the read and agent-search routes, so switching routes does not double a
// caller's allowance.
//
// Paid: per ACCOUNT, all of its keys drawing on one budget.
const (
	anonPerMinute     = 120
	anonPerDay        = 2000
	paidAPIPerMinute  = 1200
	paidDataPerMinute = 6000
)

// verifiedBotHeader is set by a Cloudflare Transform Rule when cf.client.bot is true, carrying
// a secret. The origin also listens publicly, so the header means nothing unless its value is
// the secret.
const verifiedBotHeader = "X-FH-Verified-Bot"

// keyTierTTL bounds how long a resolved key is trusted without asking again: a revoked key or
// a lapsed subscription keeps its rate for at most this long.
const keyTierTTL = time.Minute

// keyTierCacheMax bounds the cache. Unknown keys are cached too (so a flood of made-up keys
// costs one lookup each, not one per request), which makes the size attacker-chosen; past the
// bound the cache simply starts over.
const keyTierCacheMax = 10_000

// publicBudget is one public-read route class's anonymous budget before the strict switch.
type publicBudget struct {
	name      string
	perMinute int
}

var (
	publicReadBudget  = publicBudget{"publicread", publicReadsPerMinute}
	agentSearchBudget = publicBudget{"agentsearch", agentSearchPerMinute}
)

// apiKeyTiers resolves a presented key to its owner and the owner's API entitlement.
type apiKeyTiers interface {
	ResolveAPIKeyTier(ctx context.Context, tokenHash string) (db.ResolveAPIKeyTierRow, error)
}

// publicGateConfig is what the environment decides about the gate.
type publicGateConfig struct {
	// Strict applies the shared 120/min + 2,000/day anonymous budgets (PUBLIC_ANON_LIMITS=strict).
	// Off keeps each route's previous per-minute budget, so paid plans can go on sale before the
	// free tier tightens.
	Strict bool
	// BotSecret is VERIFIED_BOT_SECRET; empty disables the verified-bot exemption.
	BotSecret string
	// PricingURL is named in a 429, so a refused caller learns how to stop being refused.
	PricingURL string
}

// publicGate is the rate limiter of every public read route: one place that decides which
// class a caller is in — trusted peer, verified bot, paid key, anonymous — and which budget
// that class draws on.
type publicGate struct {
	throttler ratelimit.Throttler
	keys      apiKeyTiers
	cfg       publicGateConfig
	now       func() time.Time

	mu    sync.Mutex
	cache map[string]cachedKeyTier
}

type cachedKeyTier struct {
	row     db.ResolveAPIKeyTierRow
	known   bool
	expires time.Time
}

func newPublicGate(throttler ratelimit.Throttler, keys apiKeyTiers, cfg publicGateConfig) *publicGate {
	return &publicGate{throttler: throttler, keys: keys, cfg: cfg, now: time.Now, cache: map[string]cachedKeyTier{}}
}

// limiter is the middleware a public read route mounts for its budget class.
//
// A nil gate passes everything, the same degradation ratelimit.Middleware has for a nil
// Throttler: a test that builds a middleware set without one is not testing limits.
func (g *publicGate) limiter(b publicBudget) fiber.Handler {
	if g == nil {
		return func(c *fiber.Ctx) error { return c.Next() }
	}
	return func(c *fiber.Ctx) error {
		if ratelimit.TrustedPeer(c) || g.verifiedBot(c) {
			return c.Next()
		}
		if err := g.enforce(c, b); err != nil {
			var fe *fiber.Error
			if errors.As(err, &fe) && fe.Code == fiber.StatusTooManyRequests && g.cfg.PricingURL != "" {
				return fiber.NewError(fiber.StatusTooManyRequests,
					"too many requests, please try again later — for higher limits use an API key: "+g.cfg.PricingURL)
			}
			return err
		}
		return c.Next()
	}
}

func (g *publicGate) enforce(c *fiber.Ctx, b publicBudget) error {
	if userID, tier := g.paidTier(c); tier != billing.APITierNone {
		rate := paidAPIPerMinute
		if tier == billing.APITierData {
			rate = paidDataPerMinute
		}
		return ratelimit.Enforce(c, g.throttler, "paid:"+strconv.FormatInt(userID, 10), rate, time.Minute)
	}
	ip := c.IP()
	if !g.cfg.Strict {
		return ratelimit.Enforce(c, g.throttler, b.name+":"+ip, b.perMinute, time.Minute)
	}
	if err := ratelimit.Enforce(c, g.throttler, "anon:"+ip, anonPerMinute, time.Minute); err != nil {
		return err
	}
	return ratelimit.Enforce(c, g.throttler, "anonday:"+ip, anonPerDay, 24*time.Hour)
}

func (g *publicGate) verifiedBot(c *fiber.Ctx) bool {
	if g.cfg.BotSecret == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(c.Get(verifiedBotHeader)), []byte(g.cfg.BotSecret)) == 1
}

// paidTier is the account and API tier behind the request's key, or APITierNone when the
// request carries no key, a key of another scope, a key of an account without the product, or
// a key that could not be resolved. Only a full-scope key counts: an embed key is published
// inside third-party pages by design, and a paid rate on it would be anyone's for the copying.
func (g *publicGate) paidTier(c *fiber.Ctx) (int64, billing.APITier) {
	token, ok := auth.PresentedAPIKey(c)
	if !ok || g.keys == nil {
		return 0, billing.APITierNone
	}
	row, known := g.resolve(c.Context(), auth.HashAPIKey(token))
	if !known || row.Scope != auth.ScopeFull {
		return 0, billing.APITierNone
	}
	return row.UserID, billing.APITierOf(row.ApiUntilStripe.Time, row.ApiDataUntilStripe.Time, g.now())
}

// resolve looks a key up through the cache. The tier itself is resolved per request from the
// cached timestamps, so a subscription lapsing mid-entry is seen at once; what the cache
// delays is only a CHANGE to the account. A lookup that errored is not cached — the database
// being down must not pin a paying key to the anonymous rate for a minute after it recovers.
func (g *publicGate) resolve(ctx context.Context, hash string) (db.ResolveAPIKeyTierRow, bool) {
	now := g.now()
	g.mu.Lock()
	entry, hit := g.cache[hash]
	g.mu.Unlock()
	if hit && now.Before(entry.expires) {
		return entry.row, entry.known
	}

	row, err := g.keys.ResolveAPIKeyTier(ctx, hash)
	known := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return db.ResolveAPIKeyTierRow{}, false
	}

	g.mu.Lock()
	if len(g.cache) >= keyTierCacheMax {
		g.cache = map[string]cachedKeyTier{}
	}
	g.cache[hash] = cachedKeyTier{row: row, known: known, expires: now.Add(keyTierTTL)}
	g.mu.Unlock()
	return row, known
}
