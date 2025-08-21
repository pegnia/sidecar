# Agnostic Agones Sidecar

[![Go Report Card](https://goreportcard.com/badge/github.com/koutselakismanos/agnostic-agones-sidecar)](https://goreportcard.com/report/github.com/koutselakismanos/agnostic-agones-sidecar)
[![Docker Hub](https://img.shields.io/docker/pulls/pseudokouts/agnostic-agones-sidecar?style=for-the-badge&logo=docker)](https://hub.docker.com/r/pseudokouts/agnostic-agones-sidecar)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
![Go Version](https://img.shields.io/badge/Go-1.24+-00ADD8?style=flat&logo=go)

A universal, lightweight sidecar for integrating any network-responsive game server with [Agones](https://agones.dev) without needing source code access.

This project solves a common problem: how to run "black-box" dedicated game servers on Agones that don't have the Agones SDK built-in. This sidecar runs alongside your game server, determines its readiness by probing a network port, and manages the entire Agones SDK lifecycle for you.

---

## Key Features

-   **Truly Agnostic:** Contains zero game-specific code. It can work with Minecraft, Valheim, Terraria, or any other game server that opens a port when it's ready.
-   **Network-First Probing:** Uses a TCP or UDP network pinging strategy to determine server readiness.

## How It Works

This sidecar perfectly implements the required Agones `GameServer` lifecycle, moving your server from `Scheduled` to `Ready` and keeping it `Healthy`.

1.  **Initial Delay:** On startup, the sidecar waits for a configurable `INITIAL_DELAY`. This gives the main game server container time to start its own initialization process.
2.  **Readiness Probe:** After the delay, it enters a `ping` loop, repeatedly probing the game server's configured TCP or UDP port.
3.  **Signal Ready:** As soon as a ping is successful, the sidecar makes a one-time call to `sdk.Ready()`. This moves the Agones `GameServer` to the `Ready` state.
4.  **Health Checking:** After becoming ready, the sidecar transitions to its main role: it enters a continuous loop, calling `sdk.Health()` at a regular `HEALTH_INTERVAL`. This heartbeat is critical for letting Agones know the server is still alive.
5.  **Graceful Shutdown:** The sidecar will continue health checking until the Pod receives a termination signal (`SIGTERM`), at which point it will shut down gracefully.

## Getting Started

### Prerequisites

-   A running Kubernetes cluster.
-   [Agones](https://agones.dev/site/docs/installation/) installed on your cluster.

### Building the Container Image

```bash
# Clone the repository
git clone https://github.com/koutselakismanos/agnostic-agones-sidecar.git
cd agnostic-agones-sidecar

# Build and push the Docker image to your registry
docker build -t pseudokouts/agnostic-agones-sidecar:1.0 .
docker push pseudokouts/agnostic-agones-sidecar:1.0
```

### Configuration

The sidecar is configured entirely through environment variables.

| Environment Variable                    | Description                                     | Default Value | Required?          |
| --------------------------------------- | ----------------------------------------------- | ------------- | ------------------ |
| `AGNOSTIC_SIDECAR_INITIAL_DELAY`        | Initial delay before probing starts.            | `30s`         | No                 |
| `AGNOSTIC_SIDECAR_HEALTH_INTERVAL`      | Interval for sending health pings.              | `15s`         | No                 |
| `AGNOSTIC_SIDECAR_PING_HOST`            | Host to ping.                                   | `127.0.0.1`   | No (uses localhost)|
| `AGNOSTIC_SIDECAR_PING_PORT`            | **Port to ping.**                               | `7777`        | **Yes**            |
| `AGNOSTIC_SIDECAR_PING_PROTOCOL`        | Protocol to use for pinging.                    | `tcp`         | No (`tcp` or `udp`)|
| `AGNOSTIC_SIDECAR_PING_TIMEOUT`         | Timeout for each individual ping attempt.       | `5s`          | No                 |

## Usage Example

Below are example `GameServer` manifests demonstrating how to use the sidecar.

### Example 1: TCP Game Server (e.g., Minecraft)

```yaml
apiVersion: "agones.dev/v1"
kind: GameServer
metadata:
  generateName: minecraft-server-
spec:
  ports:
    - name: gameport
      containerPort: 25565
      protocol: TCP
  template:
    spec:
      containers:
        - name: minecraft-server
          image: itzg/minecraft-server
          env:
            - name: EULA
              value: "TRUE"
        - name: agnostic-sidecar
          image: pseudokouts/agnostic-agones-sidecar:1.0
          env:
            - name: AGNOSTIC_SIDECAR_INITIAL_DELAY
              value: "60s"
            - name: AGNOSTIC_SIDECAR_PING_PORT
              value: "25565" # Must match the containerPort
            - name: AGNOSTIC_SIDECAR_PING_PROTOCOL
              value: "tcp"```

### Example 2: UDP Game Server (e.g., Valheim)

```yaml
apiVersion: "agones.dev/v1"
kind: GameServer
metadata:
  generateName: valheim-server-
spec:
  ports:
    - name: gameport
      containerPort: 2456
      protocol: UDP
  template:
    spec:
      containers:
        - name: valheim-server
          image: lloesche/valheim-server
          env:
            - name: SERVER_NAME
              value: "My Agones Server"
            - name: WORLD_NAME
              value: "Agones"
            - name: SERVER_PASS
              value: "secret"
        - name: agnostic-sidecar
          image: pseudokouts/agnostic-agones-sidecar:1.0
          env:
            - name: AGNOSTIC_SIDECAR_INITIAL_DELAY
              value: "120s" # Valheim can take a while to start
            - name: AGNOSTIC_SIDECAR_PING_PORT
              value: "2456"
            - name: AGNOSTIC_SIDECAR_PING_PROTOCOL
              value: "udp"
```

## Contributing

Contributions are welcome! Please feel free to open an issue or submit a pull request.

1.  Fork the Project
2.  Create your Feature Branch (`git checkout -b feature/AmazingFeature`)
3.  Commit your Changes (`git commit -m 'Add some AmazingFeature'`)
4.  Push to the Branch (`git push origin feature/AmazingFeature`)
5.  Open a Pull Request

## License

Distributed under the MIT License. See `LICENSE` for more information.

## File Management API

The sidecar includes a file management API that allows you to access and manage files on the game server. This is useful for uploading mods, downloading logs, or managing configuration files without needing to restart the server.

### API Endpoints

| Endpoint | Method | Description |
| -------- | ------ | ----------- |
| `/health` | GET | Health check endpoint |
| `/api/files` | GET | List files in a directory |
| `/api/files/download` | GET | Download a file |
| `/api/files/upload` | POST | Upload a file |
| `/api/files/delete` | POST | Delete a file or directory |
| `/api/files/create-dir` | POST | Create a directory |

### Authentication

The API supports authentication using an API key. To enable authentication, set the `SIDECAR_API_KEY` environment variable. When making requests to the API, include the API key in the `X-API-Key` header.

Example:
```bash
curl -H "X-API-Key: your-api-key" http://your-server:8080/api/files?path=/data
```

If no API key is set, authentication is disabled.

### Rate Limiting

The API includes rate limiting to prevent abuse. By default, each IP address is limited to 60 requests per minute. You can adjust this limit using the `SIDECAR_RATE_LIMIT` environment variable.

### File Management Configuration

| Environment Variable | Description | Default Value |
| ------------------- | ----------- | ------------- |
| `SIDECAR_API_ADDR` | Address for the file management API server | `:8080` |
| `SIDECAR_DATA_ROOT` | Root directory for file management | `/data` |
| `SIDECAR_API_KEY` | API key for authentication (empty = no auth) | ` ` |
| `SIDECAR_RATE_LIMIT` | Rate limit (requests per minute per IP) | `60` |

### Example Usage

#### Listing Files
```bash
curl http://your-server:8080/api/files?path=/data
```

#### Downloading a File
```bash
curl http://your-server:8080/api/files/download?path=/data/config.yml -o config.yml
```

#### Uploading a File
```bash
curl -X POST -F "file=@local-file.txt" http://your-server:8080/api/files/upload?path=/data
```

#### Creating a Directory
```bash
curl -X POST -H "Content-Type: application/json" -d '{"path":"/data/mods"}' http://your-server:8080/api/files/create-dir
```

#### Deleting a File
```bash
curl -X POST -H "Content-Type: application/json" -d '{"path":"/data/old-config.yml"}' http://your-server:8080/api/files/delete
```

## Acknowledgements

-   The [Agones](https://agones.dev) team for creating an amazing open-source platform.
-   Inspiration from other sidecar projects in the Agones community.