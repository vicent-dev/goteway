# AGENTS.md

Work-in-progress HTTP API gateway in Go. Upstream: `github.com/vicent-dev/goteway`.

## Setup

`static/config.yaml` is **embedded at compile time** (`static/embed.go`) and is gitignored, so a fresh
clone will not compile until you create it:

```bash
cp static/config.example.yaml static/config.yaml
```

Values in the config support `${VAR}` and `${VAR:-default}` expansion, resolved from the process
environment plus an optional `.env` file loaded at startup. See `app/config.go` (`LoadConfig` and
`expandEnv`); `os.ExpandEnv` cannot be used because it would read `VAR:-default` as a variable name.

## Commands

```bash
make run      # go run ./cmd/server/main.go
make build    # go build ./cmd/server/main.go
make watch    # reflex hot reload (requires: go install github.com/cespare/reflex@latest)
make test     # go test -v ./...

docker compose up --build   # gateway :8080 + redis, waits for redis healthcheck
```

CI runs `make build` and `make test` on every push and PR against `main`
(`.github/workflows/go.yml`). There is no linter config yet.

## Layout

| Path              | Role                                                              |
| ----------------- | ----------------------------------------------------------------- |
| `cmd/server/`     | Entrypoint; wires SIGINT/SIGTERM into a graceful shutdown context |
| `app/`            | Server wiring: config, Redis client, routes, middleware           |
| `pkg/request/`    | `Client` (proxy + cache orchestration) and `Call` (cacheable unit) |
| `pkg/cache/`      | Generic `Cache[T Cacheable]` interface + Redis implementation      |
| `pkg/auth/`       | Accounts, sessions, JWT issuance/verification, bearer middleware  |
| `pkg/repo/`       | Generic persistence port + GORM implementation                     |
| `pkg/log/`        | `alog` wrappers that read method/path out of `context.Context`     |
| `static/`         | Embedded YAML config                                               |
| `docker/`         | Single-stage build → `/app/main`                                   |

Module path is `goteway` (the typo is intentional), so internal imports are `goteway/...`.

## Request flow

1. `loggingMiddleware` stamps method and path onto the context; it also emits
   the access log after the handler returns.
2. `rateLimiterMiddleware` is meant to be a global 5 rps / burst 10 limiter; it is in fact a no-op
   (see Known issues). `GET /health` is answered right here on the root router, before the catch-all.
3. `/auth/{register,login,refresh,logout}` go to `app/auth_handlers.go`; anything else is guarded by
   `authMiddleware` and falls through to `defaultRouteHandler`, which builds a cache-backed
   `request.Client` once at route-registration time.
4. `request.NewCall` matches the request path against configured services, rewrites the path prefix
   to the upstream host, and derives a cache key from a base64-encoded JSON fingerprint of URL +
   headers + body + method + cookies.
5. Cache miss → upstream request with a 10 s timeout → response snapshotted once, streamed to the
   client and cached asynchronously in a goroutine.

### Service matching

`services.internal` routes require a valid token; `services.external` routes do not. Matching is done
by `regexp.MatchString(path + "*", uri)`: externals are scanned first and the first match wins, then
internals override only if their path is strictly deeper, so longer paths win over shorter ones
regardless of list order (`pkg/request/findServiceConfigForUri`).

## Error handling

The pattern `pkg/auth` established, which the rest of the codebase follows:

- Each domain owns an `errors.go` of package-prefixed sentinels (`auth: …`, `request: …`,
  `cache: …`, `repo: …`). Sentinels are documented with their *intent*, not just their name —
  `auth.ErrInvalidCredentials` covers an unknown email and a wrong password on purpose, so login
  cannot enumerate accounts.
- **No package under `pkg/` decides a status code or renders an error.** HTTP *types* do appear in an
  adapter — `auth.RequireAuth` is a middleware and `auth.BearerToken` reads an `http.Header` — but
  how a rejection looks is always injected or left to `app/`. That is what `auth.RequireAuth`'s
  `onError ErrorHandler` port is for: the domain can reject a request without knowing how. The
  renderer it is handed, `unauthorizedResponse`, lives in `app/middleware.go`, not in `pkg/auth` —
  and `writeRequestError` sits in `app/route.go` for the same reason.
- Detail travels with the sentinel rather than replacing it:
  `fmt.Errorf("%w: email is required", ErrInvalidInput)`. Callers classify with `errors.Is`.
- Foreign errors are collapsed at the boundary so no caller imports the driver: `auth.parseError`
  maps jwt failures to `ErrInvalidToken`/`ErrTokenExpired`, `repo.NormalizeError` maps
  `gorm.ErrRecordNotFound` to `repo.ErrNotFound`, and the Redis cache maps `redis.Nil` to
  `cache.ErrNotFound`.
- Exactly one mapping point per domain lives in `app/`: `writeAuthError` and `writeRequestError`
  switch on `errors.Is`, and the `default` branch logs the real error and answers
  `500 {"error": "internal error"}`.
- Client-visible messages are fixed per failure class and never `err.Error()`. The proxy errors carry
  upstream hosts and dial failures, so echoing them would leak the gateway's internals.
- Anything a caller is expected to handle as a normal outcome is a sentinel rather than a failure:
  a cache miss is `cache.ErrNotFound`, and `Client.Request` treats it and a cache outage alike as
  "go upstream", logging the difference.
- Inject whatever a test needs to control: `auth.Authenticator`, `auth.Store`, `Service.now`,
  `Service.newID`, `request.Client.httpClient`.

## Conventions

- Config and route wiring live in `app/`; reusable logic lives in `pkg/` and must not import `app/`.
- Handlers are returned as closures from `s.someHandler()` methods so per-route dependencies can be
  captured at registration time.
- Errors returned to clients use `writeErrorResponse` with a JSON `{"error": ...}` body and an
  appropriate status code.
- Use the `log.Log*` helpers rather than the stdlib `log` or bare `fmt.Println`, so entries pick up
  method/path context. Never hand them `context.Background()` when a request context exists — the
  method and path that context carries are the point.
- Cache implementations satisfy `cache.Cache[T]` and operate on a `cache.Cacheable` value — the key
  and serialized value are produced by that value, not by the cache. Every method returns an error,
  so an outage is never silently read as a miss.
- **Identifiers are ULIDs, minted by the domain.** `pkg/auth.ID` is a 26-character Crockford base32
  string; every account, session and registration token record carries one, as does the `jti` inside
  a token. `Service.newID` assigns them from the service clock before anything is stored, so no
  sequence exists in the database and `Service.now` moves the id timestamps too. Consequently the
  generic `repo.Repository[T]` takes the primary key as a `string`: a `uint` there would tie a
  generic port to the first domain that used it. Two things must not drift: a record that reaches
  `GormStore` without an id is refused (`requireID`) rather than written under `''`, and a token
  `sub` claim that is not a ULID is `auth.ErrInvalidToken`, not a lookup that silently misses.
- An id that is a **credential** is not a ULID. `auth.NewOpaqueToken` stays 32 bytes of
  `crypto/rand`, because a ULID is a clock plus monotonic entropy and a registration token
  authorises exactly one account creation.
- A value shared with another goroutine and read by the handler must be snapshotted first, not shared
  as a reader. `Call.body` is the example: one read of the upstream body feeds both the cache value
  and the response the client is served.

## Known issues / TODOs

Deliberately not fixed yet, because fixing them means changing behaviour or adding features:

- The rate limiter does nothing at all: `rateLimiterMiddleware` builds `rate.NewLimiter(5, 10)` inside
  the middleware constructor, and gorilla/mux calls that constructor again on every matched request
  (`Router.Match` rebuilds the chain per request). Every request gets a fresh full bucket, so `Allow()`
  never returns false and no `429` has ever been sent. `TestRateLimiterMiddleware_Throttle` passes
  only because it calls the built handler directly. Hoisting the limiter to server state is the fix;
  it should then become per-client rather than one global 5 rps bucket, which would throttle almost
  all real traffic.
- Responses are cached regardless of method, so non-idempotent requests can be served stale.
- `NewCall` deletes `Date` from the caller's live header map, which the handler then sees. Forwarding
  now clones the map, so the deletion is contained, but the handler still observes the missing header.
- The caller's `Authorization` is deliberately not forwarded upstream, and nothing replaces it, so a
  service behind the gateway cannot tell which caller it is serving.
- `defaultRouteHandler` buffers the body, so the upstream's `Content-Length` is dropped rather than
  forwarded.
- `GET /health` is liveness only: it checks no dependency, so a gateway with every upstream down still
  reports healthy. It is registered ahead of the catch-all, so a service configured on the `health`
  path (either spelling) is unreachable.
- `server.host` is parsed into `Config` and then ignored: `http.Server.Addr` is `":" + Port`, so the
  listener always binds every interface.
- `services.internal` and `services.external` are not a public/private split. `authMiddleware` guards
  the whole proxy, so both lists require an access token; the lists differ only in who wins a tie in
  `findServiceConfigForUri`, where external wins an equal-depth tie.
- `ServicesConfig` has no `Validate()`, unlike `auth.Config`: a malformed host is discovered per
  request as a 500 rather than at startup.
- `docker-compose.yaml` forwards only `DB_HOST` to `app`, while `${VAR}` in the embedded config is
  expanded from the *container's* environment at start-up — so exporting `AUTH_ACCESS_SECRET` in the
  host shell has no effect until it is added to the `app` service's `environment:` block.
- `go.sum` is listed in `.gitignore`, which is wrong for a module that is built in CI/Docker.

Already fixed (kept here so the reasoning is not lost):

- Account, session and registration token ids were `uint` auto-increment columns, and every
  `register`/`login` response handed one to the client. Two registrations therefore disclosed the
  size of the user table, and the number kept counting. They are ULIDs now (`pkg/auth/id.go`),
  assigned by `Service.newID` before the store is called, so the enumeration is gone, `varchar(26)`
  ordering is chronological, and no sequence exists to leak. `repo.GetByID`/`Delete` moved to an
  explicit `Where("id = ?", …)` rather than `First(dest, id)`, which GORM reads as a primary key
  when it is numeric and as raw SQL when it is not. `NewJTI` is gone: the jti of a refresh token is
  a ULID too, so `replaced_by_jti` chains read in order. Not converted retroactively — `AutoMigrate`
  will not rewrite a `bigint` primary key, and every pre-existing token carries a numeric `sub` that
  is now `ErrInvalidToken`, so the database is recreated instead.
- Every proxied response reached the client as `200 OK`: `defaultRouteHandler` copied the upstream
  headers and body but never called `w.WriteHeader`. It does now, on both the live and the cached
  path — a cache hit restores the status it stored, so the two are identical. `Call.SetValue`
  rejects a cached status outside 100-999, because `WriteHeader` panics there and a payload that
  decodes into an impossible code is corrupt in the same way an undecodable one is.
- Request headers were dropped entirely: `Client.Request` built the upstream request from the method,
  URL and body only. It clones the caller's headers now, strips the hop-by-hop set and the gateway's
  own bearer, and forwards the rest. `app.copyUpstreamHeaders` does the same on the way back.
- `Call.requestUrl` was built from `r.URL.Path` only, so the query string never reached the upstream
  even though `r.URL` — which does carry it — was serialized into the cache key, giving `?page=2` its
  own entry for a byte-identical upstream request. The query is appended after the rewrite, only when
  non-empty, so no request grows a bare `?`.
- The upstream request was built with `http.NewRequest`, not `NewRequestWithContext`, so a client
  disconnect did not cancel the call in flight. It is attached now. The asynchronous cache write is
  deliberately the exception: it runs with `context.WithoutCancel(ctx)` so a caller who hangs up after
  the response was fetched whole does not throw it away.
- `GET /health` (`app/health.go`) answers `200 {"status":"ok"}` with no token, registered on the root
  router ahead of the authenticated catch-all, which is what makes it reachable by a probe. It checks
  no dependency and no service, so it is liveness only; there is no readiness endpoint.
- `/health` was reachable without a token but only as an exact `GET`, so `HEAD /health` and
  `GET /health/` were answered `401 {"error":"unauthorized"}` by `authMiddleware`. A request that misses
  a mux route is not refused — mux falls through to `PathPrefix("/")`, which matches any path *and* any
  method, so a near-miss on the health path became an authentication failure rather than a health
  answer. Both path spellings and both read methods are registered now. Anything else (`/healthz`, a
  service on a deeper path) is still proxied and still needs a token.

- Auth was a stub: `auth.IsValidToken` only checked that the `Authorization` header was non-empty,
  and `/auth/login` and `/auth/logout` were empty. It is now a real domain — gorm store, bcrypt, a
  JWT issuer with separate access/refresh secrets, refresh rotation with reuse detection.
- `pkg/request` used `errors.New` literals and returned HTTP status codes from `Client.Request`, with
  `app/` writing `err.Error()` straight to the client. It now follows the sentinel convention above.
- `pkg/cache` swallowed every Redis error, which made an outage indistinguishable from a cold cache.
  `Cache` and `Cacheable` return errors now, and `Cache.Get` reports a miss as `cache.ErrNotFound`.
- `Call.Value()` drained and closed `c.Response.Body` from the cache goroutine while the handler was
  still copying that same body to the client. The body is snapshotted once now, and `Value` no longer
  touches a body it does not own.
- `NewCall` ignored a failed read of the caller's body, which would have keyed the cache on a
  truncated request. It reports `request.ErrRequestBodyRead`.
- `app/redis.go` used `Server.Host` instead of `Redis.Host`, so `redis.host` was ignored and the
  gateway dialed `127.0.0.1` — which cannot work inside the compose `app` container.
- `RedisConfig` carried only host and port, so `app/redis.go` hardcoded `Password: ""` and `DB: 0`
  behind `@todo env` comments: an authenticated redis, and any database other than 0, were
  unreachable. `username`, `password` and `db` are configured now, and `RedisConfig.Addr()` joins
  host and port the way `DBConfig.DSN()` renders the postgres string — the section renders its own
  connection coordinates, so `redis.go` no longer imports `net` for it. None of the three new fields
  are defaulted: an empty password is what an unauthenticated redis needs, and `0` is a real
  database index rather than an unset one.
- `pkg/request/call.go` returned `&s` on a range variable; it now builds an explicit `ServiceConfig`
  copy per match.
- `app/server.go` dropped the `CancelFunc` from `context.WithTimeout`; it is now deferred.
- `docker/Dockerfile` ran `go get goteway`, which fails (`malformed module path: missing dot in
  first path element`). Removed — `go build` resolves the deps from `go.mod`.
- `docker-compose.yaml` gated `app` on `condition: service_healthy` for a `redis` service that had no
  `healthcheck` block. Added `redis-cli ping`.
