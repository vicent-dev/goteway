### Work-in-progress repo: enter at your own risk ⚒️


## Goteway

Gateway made in Go.

- Redis cache
- Embed config in a single binary output
- Microservices simple configuration
- Graceful SIGKILL

```bash 
# static/config.yaml

services:
  # auth required
  internal:
    - path: dumb-service
      host: http://localhost:8000
  external:
    - path: dumb-service/external
      host: http://localhost:8000/external
```

WIP:
- Auth system built in
- K8s integration
