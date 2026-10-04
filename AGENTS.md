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
2. `rateLimiterMiddleware` is a global 5 rps / burst 10 limiter (see Known issues).
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
  `request.Client.httpClient`.

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
- A value shared with another goroutine and read by the handler must be snapshotted first, not shared
  as a reader. `Call.body` is the example: one read of the upstream body feeds both the cache value
  and the response the client is served.

## Known issues / TODOs

Deliberately not fixed yet, because fixing them means changing behaviour or adding features:

- The rate limiter is a single global bucket, not per-client, and 5 rps will throttle almost all
  real traffic — increase it or move it to a per-IP `rate.Limiter` before treating it as protection.
- Responses are cached regardless of method, so non-idempotent requests can be served stale.
- `Client.Request` does not forward the caller's headers to the upstream, so `Authorization`,
  `Content-Type` and cookies never reach it. `NewCall` also deletes `Date` from the live header map,
  which the handler then sees.
- The upstream request is built with `http.NewRequest`, not `NewRequestWithContext`, so a client
  disconnect does not cancel the call in flight.
- `ServicesConfig` has no `Validate()`, unlike `auth.Config`: a malformed host is discovered per
  request as a 500 rather than at startup.
- Redis password and DB are hardcoded to `""` / `0` in `app/redis.go`.
- `go.sum` is listed in `.gitignore`, which is wrong for a module that is built in CI/Docker.

Already fixed (kept here so the reasoning is not lost):

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
- `pkg/request/call.go` returned `&s` on a range variable; it now builds an explicit `ServiceConfig`
  copy per match.
- `app/server.go` dropped the `CancelFunc` from `context.WithTimeout`; it is now deferred.
- `docker/Dockerfile` ran `go get goteway`, which fails (`malformed module path: missing dot in
  first path element`). Removed — `go build` resolves the deps from `go.mod`.
- `docker-compose.yaml` gated `app` on `condition: service_healthy` for a `redis` service that had no
  `healthcheck` block. Added `redis-cli ping`.
