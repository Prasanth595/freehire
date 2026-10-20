## 1. Fetch service (`services/scrapling-fetch`)

- [ ] 1.1 Request parsing and validation: required `url`, `mode` in {http, browser}, browser mode GET-only (400), `proxy: true` without a configured proxy (400), request-body cap — with unit tests
- [ ] 1.2 Bearer-token auth (401 without `SCRAPLING_FETCH_TOKEN`), `/health`, upstream-response cap, upstream status returned as data and service failures as 5xx — with tests over the starlette app and faked fetchers
- [ ] 1.3 `http` mode over `AsyncFetcher` (impersonate chrome, method/headers/body passthrough, final URL, response headers)
- [ ] 1.4 Browser session pool keyed by `(host, proxy, slot)`: reuse, idle TTL eviction, global `SCRAPLING_MAX_BROWSERS` cap with queueing, a crashed session discarded — with tests over a fake session factory
- [ ] 1.5 `browser` mode over `AsyncStealthySession` (`solve_cloudflare`, timeout) using the pool
- [ ] 1.6 Dockerfile `FROM` the pinned digest, entrypoint, README (run, env, curl example); bind `127.0.0.1`
- [ ] 1.7 CI: run the service's tests and hadolint the Dockerfile in the existing jobs

## 2. Go transport (`internal/platform/scraplingfetch`)

- [ ] 2.1 `RoundTripper`: encode the request (method, URL, headers, body, mode, proxy) to `/fetch` with the bearer token; rebuild `*http.Response` (status, headers, body, final URL) — `httptest` tests
- [ ] 2.2 Service 5xx / unreachable / malformed answer surface as transport errors — tests
- [ ] 2.3 Register the package in `internal/platform/arch/layering/blocks.go`; AGENTS.md for the package

## 3. Allowlist and wiring (`internal/ingest/sources`)

- [ ] 3.1 `NewScraplingClient(mode, proxy)` building an ordinary `*Client` over the transport, with a timeout sized for a browser fetch
- [ ] 3.2 `scraplingProviders` + `ApplyScraplingEgress` (no-op without `SCRAPLING_FETCH_URL`; fails fast when the URL is set without a token) — tests mirroring the proxy tier's
- [ ] 3.3 Call it from `cmd/ingest/main.go` in place of `ApplyFirecrawlEgress`
- [ ] 3.4 Move bayt, gulftalent, wellfound, wantapply (sitemap), hh (detail) onto the allowlist with the modes in the spec; adapt their constructors where they took `*firecrawlClient`
- [ ] 3.5 Add infojobs (browser, proxy) — removing it from `proxiedProviders` — and startupjobs, cleverstaff, kontur, tbank with their modes

## 4. Remove the hosted Firecrawl tier

- [ ] 4.1 Delete `internal/platform/firecrawl` and `firecrawltier.go`; drop it from `blocks.go`; remove `FIRECRAWL_*` from `.env.example` and config
- [ ] 4.2 Update docs: root AGENTS.md module table, `internal/ingest/sources/AGENTS.md` hosted-tier section, `hhru.go`/`proxy.go` comments that name Firecrawl

## 5. Deploy and verify

- [ ] 5.1 Build the image on host2 and start it under a systemd unit written in `freehire-ops` (`provision/host2/`); `/health` answers
- [ ] 5.2 Live-verify each allowlisted provider through the deployed service with its configured mode (one real page each); correct any row whose mode no longer passes
- [ ] 5.3 InfoJobs detail pages through browser + proxy: if they pass, restore hydration for unseen offers (with a test); otherwise record the measurement in the adapter header
- [ ] 5.4 InfoJobs parallel sessions: measure a full listing crawl's duration; set its `ingest_schedule` timeout to fit
- [ ] 5.5 Set `SCRAPLING_FETCH_URL` / `SCRAPLING_FETCH_TOKEN` on host2, deploy, and confirm each provider's next run in `board_health`
