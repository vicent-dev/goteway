### Work-in-progress repo: enter at your own risk ⚒️


## Goteway

Gateway made in Go.

- Redis cache
- Embed config in a single binary output
- Microservices simple configuration
- Graceful SIGKILL
- Rate limited to mitigate ddos attacks

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

WIP:
- Auth system built in
- K8s integration
