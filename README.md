<div align="center">

# Goteway

**A single-binary HTTP API gateway in Go.**
Path-prefix routing to internal services, a Redis response cache, and JWT authentication it owns end to end.

[![go](https://img.shields.io/badge/go-1.27-00ADD8?style=flat-square&logo=go&logoColor=white)](go.mod)
[![ci](https://github.com/vicent-dev/goteway/actions/workflows/go.yml/badge.svg)](https://github.com/vicent-dev/goteway/actions/workflows/go.yml)
[![license](https://img.shields.io/badge/license-Modified%20BSD-blue?style=flat-square)](LICENSE)

</div>

> **Early stage.** Everything in this document ships today, and every claim in it
> points at the file that implements it. The [Limitations](#limitations) section
> is not a roadmap — it is a list of what the gateway does *not* do yet. Read it
> before you deploy it.

---

## What it does

- **One binary that carries its own configuration.** `static/config.yaml` is
  compiled into the executable with `go:embed`; nothing has to be mounted
  alongside it ([`static/embed.go`](static/embed.go), [`app/config.go`](app/config.go)).
- **Routing by path prefix.** Each configured service owns a path; a request is
  forwarded to the host behind the longest matching prefix, and an unmatched
  path is a `404` instead of a guess ([`pkg/request/call.go`](pkg/request/call.go)).
- **Internal vs external services.** Services are split into two lists, and both
  require a token — the lists differ only in who wins a routing tie
  (see [Routing](#routing)).
- **Response cache in Redis, cache-aside.** A fingerprint of the request keys the
  response; a hit skips the upstream entirely, and a Redis outage degrades
  latency instead of availability ([`pkg/cache`](pkg/cache)).
- **It owns its users.** Register, log in, rotate, log out, backed by Postgres,
  with bcrypt passwords and hashed refresh tokens ([`pkg/auth`](pkg/auth)).
- **Refresh rotation with reuse detection.** Replaying a rotated refresh token
  revokes every session the account holds ([`pkg/auth/service.go`](pkg/auth/service.go)).
- **Signups are gated by one-time registration tokens**, minted by an operator
  command, so the gateway is not an open registration form
  ([`cmd/admin/genregtoken`](cmd/admin/genregtoken)).
- **Operational floor.** Panic recovery, a `10s` `ReadHeaderTimeout`, a global
  rate limit, and graceful `SIGINT`/`SIGTERM` shutdown that drains in-flight
  requests ([`app`](app)).

## Architecture

```
cmd/server ──► app ── the only adapter: HTTP, mux, Redis, GORM, YAML
                │
                ├──► pkg/request   proxy + cache orchestration (the gateway itself)
                ├──► pkg/auth      accounts, sessions, JWT issuer, bearer middleware
                ├──► pkg/cache     Cache[T] port + Redis adapter
                ├──► pkg/repo      Repository[T] port + GORM adapter
                └──► pkg/log       logging that reads method/path off the context
```

The one rule that shapes the whole codebase: **no package under `pkg/` decides a
status code or renders an error.** A domain names what went wrong with a
sentinel, and `app/` decides how that looks over the wire. `pkg/` never imports
`app/`, and never imports a driver — `redis.Nil` becomes `cache.ErrNotFound` and
`gorm.ErrRecordNotFound` becomes `repo.ErrNotFound` at the adapter, so callers
classify with `errors.Is` without knowing which database or cache is wired in.

The practical payoff: `Client.Request` returns errors, never status codes, and
every failure mode is reachable from a test without a network, a database or a
sleep.

---

## Request workflow

```
                              ┌──────────────────────────────┐
   client ──── request ──────► │ loggingMiddleware            │  stamps method + path on the
                              │                              │  context, access log on the way out
                              └──────────────┬───────────────┘
                                             ▼
                              ┌──────────────────────────────┐
│ rateLimiterMiddleware        │  intended 5 rps, burst 10, one global
                               │                              │  bucket — rebuilt per request, so it
                               │                              │  never refuses one (see Limitations)
                              └──────────────┬───────────────┘
                                             ▼
                                   gorilla/mux routing
                         ┌───────────────────┴───────────────────┐
▼                                       ▼
                    GET /health ──► 200 {"status":"ok"}     everything else ──► authMiddleware
                    liveness only, no token,             Bearer access token verified (stateless)
                    no dependency checks
                          │
    POST /auth/{register,login,refresh,logout}              │  └──► Principal in the context
    jsonMiddleware, no bearer needed                        │       missing or invalid ──► 401
                          │                                       │
                          │  register / login                     │
                          │  ──► 201 / 200 {access_token, …}     │
                          │  refresh ──► 200 new pair             ▼
                          │  logout  ──► 204, always        request.Client.Request
                          ▼                                       │
                         │                    ┌──────────────────┴───────────────────┐
                         │                    │ NewCall                                │
                         │                    │  · match the path against services    │
                         │                    │  · rewrite the prefix → upstream URL  │
                         │                    │  · fingerprint the request → cache key│
                         │                    └──────────────────┬───────────────────┘
                         │                                    no match ──► 404 not found
                         │                                       │
                         │                    ┌──────────────────▼───────────────────┐
                         │                    │ cache.Get                            │
                         │                    │  hit ──────────────────────────► serve the
                         │                    │  miss / Redis down ──► log, continue│
                         │                    └──────────────────┬───────────────────┘
                          │                                       ▼
                          │                                       │  upstream exchange, 10s timeout
                          │                                       │  dial/timeout ──► 502
                          │                                       ▼  body snapshotted once
                          │                                       │
                          │                                       ├──► streamed to the client
                          │                                       └──► goroutine: cache.Set (15s TTL)
                          ▼                                       │  a write failure is logged, not returned
                    postgres (auth tables)
```

Four things in that picture are worth stating out loud, because they are the
difference between a gateway that survives its dependencies and one that does
not:

- **The cache write is not on the response path.** By the time the snapshot is
  cached, the client already has its bytes. A Redis write failure is logged and
  dropped — it can never fail a request that has already been answered
  ([`pkg/request/client.go`](pkg/request/client.go)).
- **A cache outage is not a failure.** `Get` returns an error rather than
  pretending the key was absent, and the client treats a miss and an outage the
  same way: go upstream, and log the difference. Redis being down costs you
  latency and upstream load, not availability.
- **The snapshot is bytes, not a shared reader.** The upstream body is read once
  into a `[]byte` and the handler and the cache goroutine each get their own
  reader over it, so a response cannot be half-read by one and drained by the other.
- **Every proxied path needs a token.** The `external` list is not a public list;
  see [Routing](#routing) for what the two lists actually do.

---

## Auth

The gateway owns its users end to end: there is no external identity provider.
Tables are created on startup (`AutoMigrate`), so Postgres must be reachable
before the gateway will serve.

```
   client                     gateway                        postgres
     │                           │                                │
     │  POST /auth/register      │                                │
     ├──────────────────────────►│  validate email / username /   │
     │                           │  password, then ONE tx:      │
     │                           ├─ consume the registration ────►│  atomic, so two races
     │                           │  token (decides in the DB)     │  cannot both win
     │                           ├─ create the user (bcrypt) ─────►│
     │                           ├─ mint access + refresh ────────►│  refresh stored as SHA-256
     │  201 {access_token, …}    │◀──────────────────────────────┤
     │◀──────────────────────────┤                                │
     │                           │                                │
     │  POST /auth/login         │                                │
     ├──────────────────────────►│  bcrypt compare ───────────────►│
     │  200 {access_token, …}    │◀──────────────────────────────┤  unknown email and wrong
     │◀──────────────────────────┤                                │  password answer the same
     │                           │                                │
     │  POST /auth/refresh       │                                │
     ├──────────────────────────►│  verify signature + kind       │
     │                           ├─ look the session up by JTI ──►│  compare SHA-256 of the token
     │                           │                                │
     │                           │        already revoked?        │
     │                           │           │                    │
     │                           │           └──► revoke EVERY ───►│  a replay and a theft look
     │                           │               session of that  │  identical, so both are
     │                           │               user, 401        │  answered the same way
     │                           │           │                    │
     │                           │        transaction: mint the    │
     │                           │        new pair, mark the old   │
     │                           │        one revoked ────────────►│  replaced_by = new JTI
     │  200 new pair             │◀──────────────────────────────┤
     │◀──────────────────────────┤                                │
     │                           │                                │
     │  POST /auth/logout        │                                │
     ├──────────────────────────►│  revoke this refresh token ───►│
     │  204 (always)             │◀──────────────────────────────┤  idempotent by design
     │◀──────────────────────────┤                                │
```

| Endpoint | Body | Success | What it does |
| --- | --- | --- | --- |
| `POST /auth/register` | `email`, `username`, `password`, `registration_token` | `201` | Burns a one-time token and creates the account in the same transaction that starts the session |
| `POST /auth/login` | `email`, `password` | `200` | Returns an access/refresh pair |
| `POST /auth/refresh` | `refresh_token` | `200` | Rotates the refresh token; a replayed one revokes every session the account holds |
| `POST /auth/logout` | `refresh_token` | `204` | Revokes one session. Always `204`, whatever the token was |

A successful `register`, `login` or `refresh` answers:

```json
{
  "access_token": "eyJhbGciOiJIUzI1NiIs…",
  "refresh_token": "eyJhbGciOiJIUzI1NiIs…",
  "token_type": "Bearer",
  "expires_at": "2026-01-01T12:15:00Z",
  "user": { "id": 1, "email": "ada@example.com", "username": "ada", "role": "user", "is_active": true }
}
```

`user` is present on `register` and `login`, and omitted on a bare `refresh`.

**Token model.** Access tokens are HS256, signed with `auth.access_secret`,
default 15 minutes, and are their own proof — verifying one hits no database, so
the proxy stays stateless. Refresh tokens are signed with a *different* secret
(`auth.refresh_secret`, default 7 days); sharing one secret between the two kinds
is rejected at startup. Issuer and audience are checked on every verification, the
algorithm is pinned so a token signed with another one never reaches the key, and
`auth.clock_skew` (5s) is the leeway allowed on time checks. Only the SHA-256 of
a refresh token is persisted, so a database leak cannot be replayed against the
gateway.

**Registration tokens.** Accounts are not self-service: an operator mints a
one-time token, printed once and stored only as a hash.

```bash
make genregtoken                                    # or:
go run ./cmd/admin/genregtoken -issued-by alice -ttl 24h
```

The command loads the same embedded config as the server, so it must run where
the gateway runs.

> The rate limiter runs **before** the router, so `/auth/login` and `/health` share
> the same global 5 rps bucket as the proxy. In practice nothing is throttled at
> all today — see [Limitations](#limitations).

---

## Routing

Matching happens in `findServiceConfigForUri` (`pkg/request/call.go`). Given this
configuration:

```yaml
services:
  internal:                       # a strictly deeper path wins the tie
    - path: admin
      host: admin-service:8000
    - path: admin/reports
      host: reports-service:9000
  external:
    - path: public
      host: public-service:8000
```

these are the resolved upstreams, checked against the matcher itself rather than
reasoned about:

| Request | Resolves to | Auth flag | Upstream URL |
| --- | --- | --- | --- |
| `/public/logo.png` | `public` | external | `http://public-service:8000/logo.png` |
| `/admin/users` | `admin` | internal | `http://admin-service:8000/users` |
| `/admin/reports/daily` | `admin/reports` | internal | `http://reports-service:9000/daily` |
| `/unknown/path` | — | — | `404 not found` |

The rules, in the order they apply:

1. **Externals are scanned first and the first match wins.**
2. **Internals override only when strictly deeper**, compared by counting `/` in
   the configured path. Equal depth does not override, so a path listed in both
   lists is served by the *external* entry.
3. **The rewrite replaces the matched prefix with the host**, after dropping the
   leading `/` of the request path. An external host may itself carry a path
   (`localhost:8000/external`), which becomes a prefix of the upstream URL.
4. **Matching is a regexp with `*` appended**, i.e. `regexp.MatchString(path+"*", uri)`,
   not a literal string prefix. `public*` matches `publicity` too. Choose path
   names that cannot collide, or the rewrite will produce a nonsense URL.

`internal` and `external` are **not** a public/private split. Every proxied
request passes through `authMiddleware` and needs a valid access token either
way; `request.Client` re-checks the principal for internal paths as a second
line of defence. The lists differ only in who wins a routing tie.

### `/health`

One route is not proxied. `GET /health` answers `200 {"status":"ok"}` without a
token, and it never reaches `pkg/request`, so it is answered even with no
services configured. It is registered on the root router ahead of the catch-all,
which is what makes it reachable without credentials — and what makes a service
configured on the `health` path unreachable.

It is a liveness probe, not a readiness probe: it touches neither Redis nor
Postgres nor any upstream, so "the process is up" is all it can tell you.

---

## Response cache

`Cache[T Cacheable]` is a two-method port, and the value being stored produces
both the key and the serialized payload — so `pkg/request` decides what its own
fingerprint means, and `pkg/cache` never needs to know about HTTP.

- **Key**: base64 of a JSON fingerprint of the request URL, headers, body, method
  and cookies. `Date` is dropped from the header set before fingerprinting,
  otherwise every request would key on the second it arrived.
- **Value**: the upstream status, status code, headers, cookies and body as JSON.
- **TTL**: 15 seconds, set by the adapter.
- **Flow**: miss (or unreadable cache) → upstream → snapshot → stream to the
  client → write to the cache in a goroutine.
- **A miss and an outage are different errors.** `redis.Nil` is translated to
  `cache.ErrNotFound`, a normal outcome; a dead Redis is a real error, logged and
  then treated as a miss so the request still succeeds.
- **A corrupt entry is reported, not ignored.** A payload that will not decode
  fails the `Get` instead of producing an empty response.

What the cache is not: it does not look at the HTTP method, it has no
invalidation API, and its TTL is not configurable.

Note the asymmetry, because it is surprising: the query string **is** part of the
fingerprint (the whole `*url.URL` is serialized into the key) but is **not**
forwarded upstream. So `?page=2` buys you a separate cache entry for a request
that is byte-identical to the one without it.

---

## Configuration

`static/config.yaml` is **embedded at compile time and gitignored**, so a fresh
clone will not build until you create it:

```bash
cp static/config.example.yaml static/config.yaml
```

Any value in the file may use `${VAR}` or `${VAR:-fallback}`, resolved from the
process environment plus an optional `.env` file loaded at startup. `os.ExpandEnv`
is deliberately not used — it would read `${VAR:-default}` as a variable *named*
`VAR:-default` and always resolve to an empty string.

| Key | Env var | Default | Notes |
| --- | --- | --- | --- |
| `server.host` | — | `127.0.0.1` | ⚠️ Parsed but **unused**: the listener always binds `:port`, i.e. every interface |
| `server.port` | — | `8080` | |
| `redis.host` | — | `redis` | The compose service name; use `localhost` when running outside compose |
| `redis.port` | — | `6379` | |
| `db.host` | `DB_HOST` | `localhost` | Compose overrides this with `db` |
| `db.port` | `DB_PORT` | `5432` | |
| `db.user` | `DB_USER` | `goteway` | |
| `db.password` | `DB_PASSWORD` | `goteway` | |
| `db.name` | `DB_NAME` | `goteway` | |
| `db.sslmode` | `DB_SSLMODE` | `disable` | |
| `db.max_conns` | — | `100` | Pool ceiling |
| `db.max_idle` | — | `10` | |
| `auth.access_secret` | `AUTH_ACCESS_SECRET` | `change-me-access` | **Must differ** from the refresh secret; startup fails if it does not |
| `auth.refresh_secret` | `AUTH_REFRESH_SECRET` | `change-me-refresh` | |
| `auth.access_ttl` | — | `15m` | |
| `auth.refresh_ttl` | — | `168h` | 7 days |
| `auth.registration_token_ttl` | — | `24h` | Lifetime of `make genregtoken` output |
| `auth.issuer` | `AUTH_ISSUER` | `goteway` | Verified on every token |
| `auth.audience` | `AUTH_AUDIENCE` | `goteway-clients` | Verified on every token |
| `auth.bcrypt_cost` | — | `12` | Cost of new password hashes |
| `auth.clock_skew` | — | `5s` | Leeway on expiry checks |
| `services.internal[].path` | — | — | Path prefix; internal routes |
| `services.internal[].host` | — | — | `host:port`, optionally with a path prefix |
| `services.external[].path` | — | — | Path prefix; matched first |
| `services.external[].host` | — | — | |

There is no environment override for a bare key — `${VAR}` works because it is
written into the file, so add it yourself if you need it.


## Getting started

### With Docker Compose

Brings up the gateway, Redis and Postgres 16; the app waits for both to pass
their healthchecks.

```bash
git clone git@github.com:vicent-dev/goteway.git && cd goteway
cp static/config.example.yaml static/config.yaml
$EDITOR static/config.yaml          # set auth.access_secret / auth.refresh_secret
docker compose up --build
```

Two things about where secrets have to go, because they are easy to get wrong:

- The YAML is **embedded into the image at build time**, but `${VAR}` is expanded
  from the environment **inside the container, at start-up**. Exporting a
  variable in your shell does nothing unless compose forwards it, and
  `docker-compose.yaml` currently forwards only `DB_HOST`. So either write the
  secrets into `static/config.yaml` (gitignored, the intended place for them) or
  add them to the `app` service's `environment:` block.
- The shipped `change-me-access` / `change-me-refresh` pair **does** start: they
  are placeholders, not a check. They are public in this repository, so replace
  them with anything real — the only validated rule is that the two must differ,
  or the gateway refuses to boot.

The gateway listens on `:8080`, and `DB_USER` / `DB_PASSWORD` / `DB_NAME` are
read from your environment by compose to initialise Postgres. `DB_HOST` is set to
`db` inside the compose network.

### Locally

Needs Go 1.27, a reachable Postgres, and ideally Redis.

```bash
cp static/config.example.yaml static/config.yaml
$EDITOR static/config.yaml   # redis.host: redis -> localhost, plus your db.* and auth secrets

make install     # go mod tidy
make run         # go run ./cmd/server/main.go
make watch       # hot reload; needs: go install github.com/cespare/reflex@latest
```

Postgres is required: the gateway migrates and opens its pool at start-up and
refuses to boot without it. **Redis is not** — the client is constructed without
dialling, and a cache that cannot be reached is logged and treated as a miss, so
the gateway runs without it, just with every request paying the full upstream
cost.

`server.host` does not narrow the bind address (see the config table), so the
listener is reachable on every interface of the host.

### First request, end to end

Assuming `admin-service` is reachable at `admin-service:8000` and you configured
`- path: admin` → `host: admin-service:8000` under `services.internal`:

```bash
# 1. an operator mints a one-time registration token (-s silences make's echo,
#    so the variable holds nothing but the token)
REG_TOKEN=$(make -s genregtoken)

# 2. the account is created and a session is started in one step
curl -sS -X POST http://localhost:8080/auth/register \
  -H 'Content-Type: application/json' \
  -d "{\"email\":\"ada@example.com\",\"username\":\"ada\",
       \"password\":\"correct-horse-battery\",\"registration_token\":\"$REG_TOKEN\"}"

# 3. log in and keep the pair
curl -sS -X POST http://localhost:8080/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"ada@example.com","password":"correct-horse-battery"}' > session.json
# → {"access_token":"…","refresh_token":"…","token_type":"Bearer","expires_at":"…","user":{…}}

ACCESS_TOKEN=$(jq -r .access_token session.json)
REFRESH_TOKEN=$(jq -r .refresh_token session.json)

# 4. call an internal service with the access token
curl -sS http://localhost:8080/admin/users \
  -H "Authorization: Bearer $ACCESS_TOKEN"

# 5. when the access token expires, rotate the refresh token — the old one dies
curl -sS -X POST http://localhost:8080/auth/refresh \
  -H 'Content-Type: application/json' \
  -d "{\"refresh_token\":\"$REFRESH_TOKEN\"}" > session.json
REFRESH_TOKEN=$(jq -r .refresh_token session.json)

# 6. end the session
curl -sS -X POST http://localhost:8080/auth/logout \
  -H 'Content-Type: application/json' \
  -d "{\"refresh_token\":\"$REFRESH_TOKEN\"}" -o /dev/null -w '%{http_code}\n'   # 204
```

---

## Error model

The gateway answers a failure with a fixed, class-level message and keeps the
cause in the log. Upstream errors carry host names and dial failures, so echoing
them would leak the gateway's internals.

| Sentinel (`pkg/request`) | Status | Body |
| --- | --- | --- |
| `ErrAccessDenied` | `401` | `{"error":"unauthorized"}` |
| `ErrServiceNotFound` | `404` | `{"error":"not found"}` |
| `ErrServiceUnavailable` | `502` | `{"error":"service not available"}` |
| anything else | `500` | `{"error":"internal error"}` |

| Sentinel (`pkg/auth`) | Status | Body |
| --- | --- | --- |
| `ErrInvalidInput` | `400` | the domain message, which names the offending field |
| `ErrEmailTaken` | `409` | `auth: email already registered` |
| `ErrUserInactive` | `403` | `auth: user is not active` |
| `ErrInvalidCredentials`, `ErrMissingToken`, `ErrInvalidToken`, `ErrTokenExpired`, `ErrTokenRevoked`, `ErrTokenReused`, `ErrRegistrationTokenInvalid`, `ErrRegistrationTokenExpired` | `401` | `{"error":"unauthorized"}` — deliberately vague |
| anything else | `500` | `{"error":"internal error"}` |

Also produced by the gateway itself: `429 {"error":"rate limit reached"}` from
the limiter — a response no request has ever received, see
[Limitations](#limitations) — and `400 {"error":"invalid request body"}` for
undecodable JSON on the `/auth` endpoints. All errors are JSON with an `error`
key.

Detail travels *with* a sentinel rather than replacing it —
`fmt.Errorf("%w: email is required", ErrInvalidInput)` — so callers classify with
`errors.Is` and still get the specifics when they want them.

---

## Testing

```bash
make test              # go test -v ./...
make test-coverage     # coverage profile + browser report
```

177 tests, and the suite needs no running services: Redis is faked with
[miniredis](https://github.com/alicebob/miniredis) and the GORM stores run
against in-memory SQLite, so `make test` is hermetic. Current coverage:

| Package | Coverage |
| --- | --- |
| `pkg/cache` | 100.0% |
| `pkg/request` | 97.6% |
| `pkg/repo` | 94.1% |
| `pkg/auth` | 89.1% |
| `app` | 73.4% |

CI ([`.github/workflows/go.yml`](.github/workflows/go.yml)) runs `make install`,
`make build` and `make test` on every push and pull request to `main`, with the
Go version read from `go.mod` so it cannot drift.

---

## Limitations

What the gateway does **not** do. None of this is on the roadmap — the project is
small on purpose, and these are the boundaries of what it is.

**Proxying**

- **The caller's `Authorization` header never reaches the upstream.** Every other
  non-hop-by-hop request header is forwarded, but the bearer is a credential valid
  against this gateway and no other, so it is dropped. The gateway also has no
  caller identity of its own to forward in its place.
- **The upstream's `Content-Length` is not forwarded.** The response body is
  buffered, so its length is this handler's to declare.
- **No retries, no circuit breaker, no hedging.** A failed upstream is a `502`.
- **Responses are fully buffered**, so streaming responses and WebSockets are not
  supported.
- **No TLS listener.** Terminate TLS in front of the gateway.

**Routing**

- **Longest-prefix only, one host per path.** No weighted or round-robin balancing
  across replicas of the same service, no regex or header-based routing, no
  per-route timeouts or rewrites.
- **Matching is a regexp**, not a literal prefix — see [Routing](#routing).
- **`ServicesConfig` has no `Validate()`**, unlike the auth config: a malformed
  host is discovered per request as a `500` rather than at startup.

**Cache**

- **15-second TTL, hardcoded**, and responses are cached regardless of HTTP
  method, so non-idempotent requests can be served a stale answer.
- **No invalidation API** and no way to bust a key.
- A cache **hit** replays the snapshot — the upstream's headers, cookies, status
  code and body — so a cached response is byte-identical to the live one it came
  from.

**Rate limiting**

- **The limiter does not actually limit anything.** `rateLimiterMiddleware` creates
  its `rate.Limiter` inside the middleware constructor, and gorilla/mux calls that
  constructor again on every matched request (`Router.Match` rebuilds the chain per
  request). Every request therefore starts from a fresh, full bucket of 10, and
  `Allow()` always succeeds — no request has ever been refused with `429`. The
  standalone `TestRateLimiterMiddleware_Throttle` passes only because it calls the
  built handler directly instead of going through the router. Fixing it means
  hoisting the limiter to server state (and probably making it per-client).
- Even once wired correctly, **one global bucket at 5 rps / burst 10** shared by
  `/auth/*` and the proxy would throttle nearly all real traffic. Move it to a
  per-IP `rate.Limiter` and raise the limit before treating it as protection.

**Operations**

- **`/health` is liveness only.** It answers `200 {"status":"ok"}` with no token and
  without consulting Redis, Postgres or any configured service, so it reports that
  the process is up and nothing more — a gateway whose every upstream is down still
  reports healthy. There is no readiness endpoint to distinguish the two. Because
  it is registered on the root router ahead of the catch-all proxy, a service
  configured on the `health` path becomes unreachable.
- **No metrics, no tracing, no structured logging.** Logs are lines of the form
  `[GET] - /admin/users: …` via `pkg/log`.
- **No dynamic configuration.** The config is embedded in the binary; changing it
  means a rebuild and a restart.
- **Redis has no authentication and no database selection** — both are hardcoded.

**Not built at all**

No plugin system, no admin API or dashboard, no gRPC support, no OpenAPI
specification, no multi-tenancy, no per-route authorization (roles exist on the
user record but are not enforced).

---

## Layout

| Path | Role |
| --- | --- |
| `cmd/server/` | Entrypoint; wires `SIGINT`/`SIGTERM` into a graceful shutdown context |
| `cmd/admin/genregtoken/` | Operator command that mints one-time registration tokens |
| `app/` | Server wiring: config, Redis, Postgres, routes, middleware, error rendering |
| `pkg/request/` | `Client` (proxy + cache orchestration) and `Call` (one cacheable exchange) |
| `pkg/cache/` | Generic `Cache[T Cacheable]` interface + Redis implementation |
| `pkg/auth/` | Accounts, sessions, JWT issuance/verification, bearer middleware |
| `pkg/repo/` | Generic persistence port + GORM implementation |
| `pkg/log/` | Logging helpers that read method and path out of the `context` |
| `static/` | The embedded YAML config |

| Make target | Does |
| --- | --- |
| `make install` | `go mod tidy` |
| `make run` | `go run ./cmd/server/main.go` |
| `make build` | `go build ./cmd/server/main.go` |
| `make watch` | Hot reload via `reflex` (needs `go install github.com/cespare/reflex@latest`) |
| `make genregtoken` | Mint a one-time registration token |
| `make test` | `go test -v ./...` |
| `make test-coverage` | Coverage profile and HTML report |

`AGENTS.md` documents the conventions and the reasoning behind the error model,
including which known issues are deliberately unfixed.

## License

Modified BSD — see [LICENSE](LICENSE).