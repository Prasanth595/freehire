package handler

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"

	"github.com/strelov1/freehire/internal/api/ratelimit"
	"github.com/strelov1/freehire/internal/identity/auth"
	"github.com/strelov1/freehire/internal/platform/db"
)

// check is one Allow call a gate made.
type check struct {
	key    string
	limit  int
	window time.Duration
}

// checkRecorder admits or refuses everything and records each check it was asked.
type checkRecorder struct {
	mu     sync.Mutex
	refuse bool
	calls  []check
}

func (r *checkRecorder) Allow(_ context.Context, key string, limit int, window time.Duration) (ratelimit.Decision, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, check{key, limit, window})
	if r.refuse {
		return ratelimit.Decision{Limit: limit, ResetAfter: window, RetryAfter: window}, nil
	}
	return ratelimit.Decision{Allowed: true, Limit: limit, Remaining: limit - 1, ResetAfter: window}, nil
}

func (r *checkRecorder) got() []check {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]check(nil), r.calls...)
}

// fakeKeyTiers resolves token hashes to rows and counts lookups.
type fakeKeyTiers struct {
	mu      sync.Mutex
	rows    map[string]db.ResolveAPIKeyTierRow
	err     error
	lookups int
}

func (f *fakeKeyTiers) ResolveAPIKeyTier(_ context.Context, hash string) (db.ResolveAPIKeyTierRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups++
	if f.err != nil {
		return db.ResolveAPIKeyTierRow{}, f.err
	}
	row, ok := f.rows[hash]
	if !ok {
		return db.ResolveAPIKeyTierRow{}, pgx.ErrNoRows
	}
	return row, nil
}

var gateNow = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

const (
	apiKeyToken  = "fhk_api"
	dataKeyToken = "fhk_data"
	embedToken   = "fhk_embed"
	unpaidToken  = "fhk_unpaid"
	gateIP       = "203.0.113.9"
)

func paidKeys() *fakeKeyTiers {
	future := gateNow.Add(24 * time.Hour)
	return &fakeKeyTiers{rows: map[string]db.ResolveAPIKeyTierRow{
		auth.HashAPIKey(apiKeyToken):  {UserID: 7, Scope: auth.ScopeFull, ApiUntilStripe: ts(future)},
		auth.HashAPIKey(dataKeyToken): {UserID: 8, Scope: auth.ScopeFull, ApiDataUntilStripe: ts(future)},
		auth.HashAPIKey(embedToken):   {UserID: 7, Scope: auth.ScopeEmbed, ApiUntilStripe: ts(future)},
		auth.HashAPIKey(unpaidToken):  {UserID: 9, Scope: auth.ScopeFull},
	}}
}

func newTestGate(th ratelimit.Throttler, keys apiKeyTiers, strict bool, botSecret string) *publicGate {
	g := newPublicGate(th, keys, publicGateConfig{Strict: strict, BotSecret: botSecret, PricingURL: "https://freehire.me/pricing#api"})
	g.now = func() time.Time { return gateNow }
	return g
}

// gateApp mounts the gate's two budgets on two routes, the way the read and agent-search
// routes mount them.
func gateApp(g *publicGate) *fiber.App {
	app := fiber.New(fiber.Config{ProxyHeader: fiber.HeaderXForwardedFor})
	ok := func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) }
	app.Get("/read", g.limiter(publicReadBudget), ok)
	app.Get("/agent", g.limiter(agentSearchBudget), ok)
	return app
}

func gateGet(t *testing.T, app *fiber.App, path string, headers map[string]string) (int, string) {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), fiber.MethodGet, path, nil)
	req.Header.Set(fiber.HeaderXForwardedFor, gateIP)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func bearer(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

func TestPublicGate_LegacyAnonymousBudgetsUntilStrict(t *testing.T) {
	rec := &checkRecorder{}
	app := gateApp(newTestGate(rec, paidKeys(), false, ""))
	gateGet(t, app, "/read", nil)
	gateGet(t, app, "/agent", nil)
	want := []check{
		{"publicread:" + gateIP, publicReadsPerMinute, time.Minute},
		{"agentsearch:" + gateIP, agentSearchPerMinute, time.Minute},
	}
	if got := rec.got(); !equalChecks(got, want) {
		t.Fatalf("checks = %+v, want %+v", got, want)
	}
}

func TestPublicGate_StrictAnonymousIsSharedPerMinuteAndPerDay(t *testing.T) {
	rec := &checkRecorder{}
	app := gateApp(newTestGate(rec, paidKeys(), true, ""))
	gateGet(t, app, "/read", nil)
	gateGet(t, app, "/agent", nil)
	one := []check{
		{"anon:" + gateIP, anonPerMinute, time.Minute},
		{"anonday:" + gateIP, anonPerDay, 24 * time.Hour},
	}
	if got := rec.got(); !equalChecks(got, append(one, one...)) {
		t.Fatalf("checks = %+v, want the same two shared keys on both routes", got)
	}
	if anonPerMinute != 120 || anonPerDay != 2000 {
		t.Fatalf("anonymous budgets = %d/min %d/day, the spec says 120 and 2000", anonPerMinute, anonPerDay)
	}
}

func TestPublicGate_PaidKeysAreLimitedPerAccountAtTheirTier(t *testing.T) {
	cases := []struct {
		name  string
		token string
		want  check
	}{
		{"api tier", apiKeyToken, check{"paid:7", paidAPIPerMinute, time.Minute}},
		{"data tier", dataKeyToken, check{"paid:8", paidDataPerMinute, time.Minute}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &checkRecorder{}
			app := gateApp(newTestGate(rec, paidKeys(), true, ""))
			gateGet(t, app, "/read", bearer(tc.token))
			gateGet(t, app, "/agent", bearer(tc.token))
			if got := rec.got(); !equalChecks(got, []check{tc.want, tc.want}) {
				t.Fatalf("checks = %+v, want one per-account budget across both routes", got)
			}
		})
	}
	if paidAPIPerMinute != 1200 || paidDataPerMinute != 6000 {
		t.Fatalf("paid budgets = %d / %d, the spec says 1200 and 6000", paidAPIPerMinute, paidDataPerMinute)
	}
}

func TestPublicGate_KeysWithoutAPaidFullScopeAreAnonymous(t *testing.T) {
	for _, token := range []string{embedToken, unpaidToken, "fhk_unknown"} {
		t.Run(token, func(t *testing.T) {
			rec := &checkRecorder{}
			app := gateApp(newTestGate(rec, paidKeys(), true, ""))
			gateGet(t, app, "/read", bearer(token))
			got := rec.got()
			if len(got) == 0 || got[0].key != "anon:"+gateIP {
				t.Fatalf("checks = %+v, want the anonymous budget", got)
			}
		})
	}
}

func TestPublicGate_ResolvedTiersAreCachedFailuresAreNot(t *testing.T) {
	keys := paidKeys()
	app := gateApp(newTestGate(&checkRecorder{}, keys, true, ""))
	gateGet(t, app, "/read", bearer(apiKeyToken))
	gateGet(t, app, "/read", bearer(apiKeyToken))
	gateGet(t, app, "/read", bearer("fhk_unknown"))
	gateGet(t, app, "/read", bearer("fhk_unknown"))
	if keys.lookups != 2 {
		t.Fatalf("lookups = %d, want 2 (one per distinct key, negatives cached too)", keys.lookups)
	}

	broken := &fakeKeyTiers{err: errors.New("database is down")}
	rec := &checkRecorder{}
	app = gateApp(newTestGate(rec, broken, true, ""))
	if code, _ := gateGet(t, app, "/read", bearer(apiKeyToken)); code != fiber.StatusOK {
		t.Fatalf("a failed lookup answered %d, want the request served as anonymous", code)
	}
	gateGet(t, app, "/read", bearer(apiKeyToken))
	if broken.lookups != 2 {
		t.Fatalf("lookups = %d, want 2 — a failure must not be cached", broken.lookups)
	}
	if got := rec.got(); got[0].key != "anon:"+gateIP {
		t.Fatalf("checks = %+v, want anonymous", got)
	}
}

func TestPublicGate_AnonymousRequestsCostNoLookup(t *testing.T) {
	keys := paidKeys()
	app := gateApp(newTestGate(&checkRecorder{}, keys, true, ""))
	gateGet(t, app, "/read", nil)
	gateGet(t, app, "/read", map[string]string{"Authorization": "Bearer a.session.jwt"})
	if keys.lookups != 0 {
		t.Fatalf("lookups = %d, want 0", keys.lookups)
	}
}

func TestPublicGate_VerifiedBots(t *testing.T) {
	const secret = "s3cret"
	cases := []struct {
		name      string
		secret    string
		header    string
		wantCheck bool
	}{
		{"matching secret is exempt", secret, secret, false},
		{"forged value is anonymous", secret, "guess", true},
		{"no secret configured is anonymous", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &checkRecorder{}
			app := gateApp(newTestGate(rec, paidKeys(), true, tc.secret))
			gateGet(t, app, "/read", map[string]string{verifiedBotHeader: tc.header})
			if got := len(rec.got()) > 0; got != tc.wantCheck {
				t.Fatalf("budget checked = %v, want %v", got, tc.wantCheck)
			}
		})
	}
}

func TestPublicGate_RefusalNamesThePaidPlans(t *testing.T) {
	app := gateApp(newTestGate(&checkRecorder{refuse: true}, paidKeys(), true, ""))
	code, body := gateGet(t, app, "/read", nil)
	if code != fiber.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", code)
	}
	if !strings.Contains(body, "https://freehire.me/pricing#api") {
		t.Fatalf("body %q does not point to the API plans", body)
	}
}

func TestPublicGate_StrictDayCapRefusesAfterTheMinutePasses(t *testing.T) {
	rec := &dayRefuser{}
	app := gateApp(newTestGate(rec, paidKeys(), true, ""))
	if code, _ := gateGet(t, app, "/read", nil); code != fiber.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 from the daily budget", code)
	}
}

// dayRefuser admits every per-minute check and refuses every daily one.
type dayRefuser struct{}

func (dayRefuser) Allow(_ context.Context, key string, limit int, window time.Duration) (ratelimit.Decision, error) {
	if strings.HasPrefix(key, "anonday:") {
		return ratelimit.Decision{Limit: limit, ResetAfter: window, RetryAfter: window}, nil
	}
	return ratelimit.Decision{Allowed: true, Limit: limit, Remaining: limit, ResetAfter: window}, nil
}

func equalChecks(a, b []check) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
