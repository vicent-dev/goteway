### Work-in-progress repo: enter at your own risk ⚒️


## Goteway

Gateway made in Go.

- Redis cache
- Embed config in a single binary output
- Microservices simple configuration
- Graceful SIGKILL
- Rate limited to mitigate ddos attacks
- Postgres backed auth: register, login, refresh with rotation, logout

```bash 
# static/config.yaml

services:
  # auth required
  internal:
    - path: dumb-service
      host: localhost:8000
    - path: ${MICROSERVICE_SERVICE_PATH}
      host: ${MICROSERVICE_SERVICE_HOST}
  external:
    - path: dumb-service 
      host: localhost:8000/external
```

## Auth

The gateway owns its users. Tables are created on startup, so PostgreSQL has to be reachable.

| Endpoint                | What it does                                                        |
| ----------------------- | ------------------------------------------------------------------- |
| `POST /auth/register`   | Consumes a one-time registration token and creates the user          |
| `POST /auth/login`      | Returns an access/refresh pair                                       |
| `POST /auth/refresh`    | Rotates the refresh token; replaying an old one revokes every session |
| `POST /auth/logout`     | Revokes a refresh token, always answering `204`                      |

Every proxied path requires `Authorization: Bearer <access token>`.

Registrations need a registration token, minted by an operator:

```bash
make genregtoken   # or: go run ./cmd/admin/genregtoken -issued-by alice
```

The token is printed once and only its hash is stored.
