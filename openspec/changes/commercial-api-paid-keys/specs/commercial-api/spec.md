## ADDED Requirements

### Requirement: API subscription is a separate product

The system SHALL sell API access as two Stripe prices — API and Data — configured apart from the
Pro and Ultra prices. Buying an API price SHALL open or change only an API subscription, and buying
a Pro or Ultra price SHALL open or change only a Pro/Ultra subscription.

#### Scenario: Pro subscriber buys API
- **WHEN** an account with a live Pro subscription starts checkout for the API price
- **THEN** a new Checkout Session for the API price is opened and the Pro subscription is not modified

#### Scenario: API subscriber buys Pro
- **WHEN** an account with a live API subscription and no Pro/Ultra subscription starts checkout for Pro
- **THEN** a new Checkout Session for Pro is opened and the API subscription is not modified

#### Scenario: API subscriber upgrades to Data
- **WHEN** an account with a live API subscription starts checkout for the Data price
- **THEN** the existing API subscription's price is changed to Data in place, and no second API subscription is opened

#### Scenario: Unconfigured price is refused
- **WHEN** checkout is requested for a price in none of the configured lists
- **THEN** the request is refused and nothing is opened

### Requirement: API entitlement is recorded per tier

The system SHALL record, per account, how far a live API subscription and a live Data subscription
reach, written by the same webhook apply path and `billing-sync` reconcile that maintain Pro and
Ultra. The account's API tier SHALL resolve to Data while Data reaches past now, otherwise to API
while API reaches past now, otherwise to none.

#### Scenario: Webhook grants the tier
- **WHEN** a Stripe event for a customer whose subscription carries the API price is applied
- **THEN** the account's API entitlement is set to that subscription's period end and its tier resolves to API

#### Scenario: Lapsed subscription resolves to none
- **WHEN** an account's recorded API and Data entitlements are both in the past
- **THEN** its tier resolves to none

#### Scenario: API purchase does not grant Pro
- **WHEN** an account's only subscription carries an API or Data price
- **THEN** its plan tier (`plan.TierOf`) is free

#### Scenario: Reconcile reaches API subscribers
- **WHEN** `billing-sync` runs and an account's API or Data entitlement lapses within its window
- **THEN** that account is re-read from Stripe

### Requirement: Paid keys are limited per account at the tier's rate

On every public read route, a request carrying a `full`-scope API key whose owner's API tier is not
none SHALL be limited per account at that tier's rate (API: 1,200 requests/minute; Data: 6,000
requests/minute) and SHALL NOT be subject to the anonymous limits. Every other request, including
one carrying a key of another scope or a key whose owner has no API tier, SHALL be limited as
anonymous.

#### Scenario: Paid key is not throttled at the anonymous rate
- **WHEN** a caller with a live API tier sends 500 public reads in one minute with its full-scope key
- **THEN** every request is answered without 429

#### Scenario: Paid account shares one budget across its keys
- **WHEN** one account sends requests with two different full-scope keys
- **THEN** both draw on the same per-account budget

#### Scenario: Embed key gets no paid rate
- **WHEN** a caller sends public reads with an embed-scope key of an account holding a live API tier
- **THEN** the requests are limited as anonymous

#### Scenario: Key of an unpaid account is anonymous
- **WHEN** a caller sends public reads with a full-scope key whose account has no API tier
- **THEN** the requests are limited as anonymous

#### Scenario: Key lookup failure degrades to anonymous
- **WHEN** resolving the presented key fails because the database is unavailable
- **THEN** the request is limited as anonymous rather than refused

### Requirement: Anonymous public reads are bounded per minute and per day

A public read route request that is not a paid key and not a verified bot SHALL be limited per IP
to 120 requests per minute and 2,000 requests per day. Both budgets SHALL be shared by the public
read and agent-search routes. The suggest and talent catalogue budgets are unchanged. These limits
apply when `PUBLIC_ANON_LIMITS=strict`; while it is unset, anonymous callers keep the previous
per-route budgets (600/min reads, 300/min agent search, no daily cap) so the paid plans can go on
sale before the free tier tightens.

#### Scenario: Legacy budgets until switched
- **WHEN** `PUBLIC_ANON_LIMITS` is unset and one IP sends 500 anonymous reads within one minute
- **THEN** none is refused

#### Scenario: Per-minute cap
- **WHEN** one IP sends its 121st anonymous public read within one minute
- **THEN** it receives 429 with `Retry-After`

#### Scenario: Daily cap across routes
- **WHEN** one IP has made 2,000 anonymous requests in a day split between `/jobs/search` and `/agent/jobs/search`
- **THEN** its next anonymous request to either route receives 429

#### Scenario: The refusal names the paid tier
- **WHEN** an anonymous public read is refused with 429
- **THEN** the error message points to the API plans page

### Requirement: Verified bots are exempt from the anonymous limits

A request carrying the verified-bot header whose value equals the configured secret SHALL skip the
anonymous limits. A request whose header value does not equal the secret, or any request when no
secret is configured, SHALL be treated as anonymous. The comparison SHALL be constant-time.

#### Scenario: Cloudflare-verified bot passes
- **WHEN** a request carries the verified-bot header with the configured secret
- **THEN** it is not counted against any anonymous budget

#### Scenario: Forged header is ignored
- **WHEN** a request carries the verified-bot header with any other value
- **THEN** it is limited as anonymous

#### Scenario: No secret configured
- **WHEN** the secret is unset and a request carries the verified-bot header
- **THEN** it is limited as anonymous

### Requirement: Paid keys carry a data licence

The Terms SHALL state that an account holding a live API tier may store the data it reads and
display it in its own product, SHALL NOT resell or redistribute the raw data as a dataset, and
SHALL link each displayed posting to its freehire page or credit "via freehire.me". The pricing
page SHALL offer the API and Data plans, and the OpenAPI document SHALL declare bearer-key
authentication and the anonymous and paid limits.

#### Scenario: Licence is published
- **WHEN** a visitor opens `/terms`
- **THEN** a data licence section states the store/display allowance, the resale prohibition and the attribution rule

#### Scenario: Plans are purchasable
- **WHEN** a signed-in visitor opens `/pricing` and chooses an API plan
- **THEN** they are sent to checkout for that plan's price
