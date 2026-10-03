# AGENTS.md

Work-in-progress HTTP API gateway in Go. Upstream: `github.com/vicent-dev/goteway`.

## Setup

`static/config.yaml` is **embedded at compile time** (`static/embed.go`) and is gitignored, so a fresh
clone will not compile until you create it:

```bash
cp static/config.example.yaml static/config.yaml
```

Values in the config support `${VAR}` expansion, resolved from the process environment plus an
optional `.env` file loaded at startup. See `app/config.go:55`.

## Commands

```bash
make run      # go run ./cmd/server/main.go
make build    # go build ./cmd/server/main.go
make watch    # reflex hot reload (requires: go install github.com/cespare/reflex@latest)

docker compose up --build   # gateway :8080 + redis, waits for redis healthcheck
```

There is no test suite, linter config, or CI yet.

## Layout

| Path              | Role                                                              |
| ----------------- | ----------------------------------------------------------------- |
| `cmd/server/`     | Entrypoint; wires SIGINT/SIGTERM into a graceful shutdown context |
| `app/`            | Server wiring: config, Redis client, routes, middleware           |
| `pkg/request/`    | `Client` (proxy + cache orchestration) and `Call` (cacheable unit) |
| `pkg/cache/`      | Generic `Cache[T Cacheable]` interface + Redis implementation      |
| `pkg/auth/`       | Bearer token context key and validation stub                       |
| `pkg/log/`        | `alog` wrappers that read method/path out of `context.Context`     |
| `static/`         | Embedded YAML config                                               |
| `docker/`         | Single-stage build → `/app/main`                                   |

Module path is `goteway` (the typo is intentional), so internal imports are `goteway/...`.

## Request flow

1. `loggingMiddleware` stamps method, path, and the `Authorization` header onto the context; it also
   emits the access log after the handler returns.
2. `rateLimiterMiddleware` is a global 5 rps / burst 10 limiter (see Known issues).
3. `/auth/login` and `/auth/logout` are stubs returning nothing.
4. Everything else falls through to `defaultRouteHandler`, which builds a cache-backed
   `request.Client` once at route-registration time.
5. `request.NewCall` matches the request path against configured services, rewrites the path prefix
   to the upstream host, and derives a cache key from a base64-encoded JSON fingerprint of URL +
   headers + body + method + cookies.
6. Cache miss → upstream request with a 10 s timeout → response streamed to the client, and cached
   asynchronously in a goroutine.

### Service matching

`services.internal` routes require a valid token; `services.external` routes do not. Matching is done
by `regexp.MatchString(path + "*", uri)`: externals are scanned first and the first match wins, then
internals override only if their path is strictly deeper, so longer paths win over shorter ones
regardless of list order (`pkg/request/call.go:85`).

## Conventions

- Config and route wiring live in `app/`; reusable logic lives in `pkg/` and must not import `app/`.
- Handlers are returned as closures from `s.someHandler()` methods so per-route dependencies can be
  captured at registration time.
- Errors returned to clients use `writeErrorResponse` with a JSON `{"error": ...}` body and an
  appropriate status code; upstream failures deliberately mask internals as
  `"service not available"`.
- Use the `log.Log*` helpers rather than the stdlib `log` or bare `fmt.Println`, so entries pick up
  method/path context.
- Cache implementations satisfy `cache.Cache[T]` and operate on a `cache.Cacheable` value — the key
  and serialized value are produced by that value, not by the cache.

## Known issues / TODOs

Deliberately not fixed yet, because fixing them means changing behaviour or adding features:

- Auth is a stub: `auth.IsValidToken` only checks that the `Authorization` header is non-empty.
- `/auth/login` and `/auth/logout` handlers are empty.
- The rate limiter is a single global bucket, not per-client, and 5 rps will throttle almost all
  real traffic — increase it or move it to a per-IP `rate.Limiter` before treating it as protection.
- Responses are cached regardless of method, so non-idempotent requests can be served stale.
- `pkg/cache` swallows all Redis errors; the `Cache` interface has no error return, so failures are
  silent cache misses rather than something a caller could react to.
- Redis password and DB are hardcoded to `""` / `0` in `app/redis.go`.
- `go.sum` is listed in `.gitignore`, which is wrong for a module that is built in CI/Docker.

Already fixed (kept here so the reasoning is not lost):

- `app/redis.go` used `Server.Host` instead of `Redis.Host`, so `redis.host` was ignored and the
  gateway dialed `127.0.0.1` — which cannot work inside the compose `app` container.
- `pkg/request/call.go` returned `&s` on a range variable; it now builds an explicit `ServiceConfig`
  copy per match.
- `app/server.go` dropped the `CancelFunc` from `context.WithTimeout`; it is now deferred.
- `docker/Dockerfile` ran `go get goteway`, which fails (`malformed module path: missing dot in
  first path element`). Removed — `go build` resolves the deps from `go.mod`.
- `docker-compose.yaml` gated `app` on `condition: service_healthy` for a `redis` service that had no
  `healthcheck` block. Added `redis-cli ping`.
