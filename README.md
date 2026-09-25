# Pegnia Sidecar

![Go Version](https://img.shields.io/badge/Go-1.24+-00ADD8?style=flat&logo=go)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

A small HTTP service that runs next to a game server container and gives the Pegnia panel
access to the server's files and console log. It contains no game-specific code and does
not talk to Kubernetes.

The Pegnia controller adds it to every game server pod when `SIDECAR_IMAGE` is set in the
controller's configuration. The sidecar shares the game's data volume and is reachable at
`<server>-0.<server>.<namespace>.svc:9999` through the server's headless Service.

Readiness of the game itself is **not** the sidecar's job: the controller puts a Kubernetes
readiness probe on the game container (a TCP check on the first TCP game port). Earlier
versions of this project drove the Agones SDK lifecycle; that code has been removed.

## API

All paths are relative to the data root. `/`, `""` and `.` mean the root itself;
`/mods` and `mods` are the same directory. Paths that would leave the root (`..`,
`/../etc`, `../data-other`, ...) are rejected, and every file operation goes through
Go's `os.Root`, so symlinks inside the data directory cannot lead outside it either.

| Endpoint                | Method | Description                                           |
|-------------------------|--------|-------------------------------------------------------|
| `/health`               | GET    | Liveness check; never requires an API key             |
| `/api/files?path=`      | GET    | List a directory (JSON)                               |
| `/api/files/download?path=` | GET | Download a file                                      |
| `/api/files/upload?path=[&overwrite=true]` | POST | Upload a multipart `file` into a directory |
| `/api/files/delete`     | POST   | Delete a file or directory: `{"path": "..."}`         |
| `/api/files/create-dir` | POST   | Create a directory and its parents: `{"path": "..."}` |
| `/api/logs/stream`      | GET    | Server-sent events: the last 100 lines of the console log, then new lines as they are written |

Uploads are limited to 500 MB, and files with executable/script extensions
(`.exe`, `.sh`, `.php`, `.js`, ...) are refused. Uploading over an existing file needs
`overwrite=true`.

### Authentication

If `SIDECAR_API_KEY` is set, every request except `/health` must carry it in the
`X-API-Key` header, otherwise the sidecar answers `401`. If it is empty, the API is open
and access must be limited at the network level (e.g. a NetworkPolicy that only lets the
panel reach port 9999).

### Rate limiting

Each client IP (the first `X-Forwarded-For` entry if present, else the remote address) is
limited to `SIDECAR_RATE_LIMIT` requests per minute. `/health` is not limited.

## Configuration

| Environment variable  | Description                                          | Default           |
|-----------------------|------------------------------------------------------|-------------------|
| `SIDECAR_API_ADDR`    | Listen address of the API                            | `:9999`           |
| `SIDECAR_DATA_ROOT`   | Directory served by the API (the game's data volume); must exist | `/data` |
| `SIDECAR_STDOUT_FILE` | Console log file, relative to the data root          | `logs/stdout.log` |
| `SIDECAR_API_KEY`     | Required `X-API-Key` value (the controller sets a per-server key) | (empty: refuses to start) |
| `SIDECAR_INSECURE`    | `true` allows starting without an API key (local experiments only) | (empty) |
| `SIDECAR_RATE_LIMIT`  | Requests per minute per client IP                    | `60`              |

## Examples

```bash
curl http://localhost:9999/api/files?path=/
curl -o server.properties 'http://localhost:9999/api/files/download?path=server.properties'
curl -X POST -F "file=@my-mod.jar" 'http://localhost:9999/api/files/upload?path=mods'
curl -X POST -H 'Content-Type: application/json' -d '{"path":"mods/old"}' http://localhost:9999/api/files/create-dir
curl -X POST -H 'Content-Type: application/json' -d '{"path":"mods/old"}' http://localhost:9999/api/files/delete
curl -N http://localhost:9999/api/logs/stream
# with SIDECAR_API_KEY set:
curl -H "X-API-Key: $SIDECAR_API_KEY" http://localhost:9999/api/files
```

## Development

```bash
go build ./... && go vet ./... && go test ./...
docker compose up --build   # a Minecraft server with the sidecar on localhost:9999
make build                  # build the container image (see Makefile for the image name)
```

## License

Distributed under the MIT License. See `LICENSE` for more information.
