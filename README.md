# mcp-estuary

`mcp-estuary` is a minimal MCP gateway that exposes multiple stdio-based MCP servers as one streamable HTTP `/mcp` endpoint.

Japanese README: [README-JP.md](https://github.com/Melon-cream/mcp-estuary/blob/main/README-JP.md)

## Quick Startup

Write `mcpe.json` in the same shape as `mcp.json`.

```json
{
  "mcpServers": {
    "fetch": {
      "command": "uvx",
      "args": ["mcp-server-fetch"]
    },
    "memory": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-memory"],
      "env": {
        "MEMORY_FILE_PATH": ".memory/memory.json"
      }
    },
    "tavily": {
      "command": "npx",
      "args": ["-y", "tavily-mcp@latest"],
      "env": {
        "TAVILY_API_KEY": "${TAVILY_API_KEY}"
      }
    }
  }
}
```

Start the gateway.

```bash
mcpe serve --config mcpe.json --listen 127.0.0.1:8080
```

Point your client at `mcp-estuary`.

```json
{
  "mcpServers": {
    "estuary": {
      "command": "npx",
      "args": [
        "-y",
        "mcp-remote",
        "http://127.0.0.1:8080/mcp"
      ]
    }
  }
}
```

Just use.

Claude example:

```json
{
  "mcpServers": {
    "estuary": {
      "command": "npx",
      "args": [
        "-y",
        "mcp-remote",
        "http://127.0.0.1:8080/mcp"
      ]
    }
  }
}
```

Codex example:

```json
{
  "mcpServers": {
    "estuary": {
      "command": "npx",
      "args": [
        "-y",
        "mcp-remote",
        "http://127.0.0.1:8080/mcp"
      ]
    }
  }
}
```

## Feature Matrix

| Feature | Supported |
| --- | --- |
| Single `/mcp` streamable HTTP gateway | ☑ |
| `uvx` / `npx` / `docker` runtimes | ☑ |
| `.env` expansion in `mcpe.json` | ☑ |
| Relative `_PATH` resolution after expansion | ☑ |
| Auto-repair for trailing commas in `mcpe.json` | ☑ |
| Background launch by default | ☑ |
| `mcpe config set --systemd enable/disable` | ☑ |
| Config hot reload | ☑ |
| `mcpe doctor` | ☑ |
| `mcpe status` | ☑ |
| `mcpe logs -f` / `--follow` | ☑ |

## Requirements

- Go 1.26+
- One or more runtime dependencies used by your servers:
  - `uv`
  - `node` / `npm`
  - `docker`

## Configuration Details

`mcpe.json` is loaded from the current directory by default.

Environment handling:

- `${VAR}` references inside `env` are expanded from process environment variables first, then from `.env` next to `mcpe.json`.
- Undefined `${VAR}` references make that server invalid. Other valid servers still load.
- `_PATH` values are resolved after expansion, relative to the directory containing `mcpe.json`.
- `MCPE_HOME` also reads from the same `.env` file when it is not already set in the process environment.
- Docker-based MCP servers do not receive host environment variables automatically. Pass them explicitly in `docker run` args or container configuration.

Config repair:

- `mcpe.json` with trailing commas is repaired automatically.
- The repaired diff is written to the gateway log.

Hot reload:

- `mcpe serve` polls `mcpe.json` and reloads it automatically.
- Invalid changes are isolated. Healthy servers continue serving.

## Commands

```text
mcpe serve [--config PATH] [--use NAME ...] [--install-concurrency N] [--listen ADDR] [--foreground]
mcpe stop [--config PATH]
mcpe logs [--server NAME] [--follow]
mcpe doctor [--config PATH] [--use NAME ...]
mcpe status [--config PATH]
mcpe cache clean
mcpe config set --install-concurrency N
mcpe config set --systemd enable|disable [--config PATH] [--listen ADDR]
mcpe --help
```

Install concurrency precedence:

1. `INSTALL_CONCURRENCY`
2. `serve --install-concurrency`
3. `mcpe config set --install-concurrency`
4. default `2`

## Status Directory

Runtime state is stored under `~/.mcp-estuary` by default.

- gateway log: `~/.mcp-estuary/logs/gateway.log`
- server log: `~/.mcp-estuary/logs/servers/<name>.log`
- managed workdir: `~/.mcp-estuary/mcp-servers/<name>`
- runtime status: `~/.mcp-estuary/run/runtime-status.json`

When `mcpe serve` starts, it also creates `mcp-servers-logs` next to `mcpe.json` as a symlink to the server log directory.

`mcpe status` also shows whether `mcpe.service` is registered, enabled, and active in user systemd.

## HTTP Endpoints

- `POST /mcp`
- `GET /mcp`
- `DELETE /mcp`
- `GET /healthz`

`POST /mcp` supports:

- `initialize`
- `notifications/initialized`
- `ping`
- `tools/list`
- `tools/call`

Exposed tool names are rewritten as `<server>__<tool>`.

## Docker

Build:

```bash
docker build -t mcp-estuary .
```

Run:

```bash
docker run --rm -p 8080:8080 \
  -v "$(pwd)/mcpe.json:/workspace/mcpe.json:ro" \
  -v "$(pwd)/.memory:/workspace/.memory" \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v mcp-estuary-data:/root/.mcp-estuary \
  mcp-estuary
```

`/var/run/docker.sock` is only required when the gateway must launch Docker-based MCP servers.

## Docker Compose

`compose.yaml` includes:

- published port `8080`
- `mcpe.json` mount
- `.memory` bind mount
- persistent volume for `/root/.mcp-estuary`
- `/var/run/docker.sock` mount for Docker-based MCP servers

Start:

```bash
docker compose up --build
```

## Example

Minimal Docker MCP example:

- [examples/hello-docker-mcp/Dockerfile](examples/hello-docker-mcp/Dockerfile)
- [examples/hello-docker-mcp/mcpe.json](examples/hello-docker-mcp/mcpe.json)

```json
{
  "mcpServers": {
    "hello-docker": {
      "command": "docker",
      "args": ["run", "-i", "--rm", "mcpe-hello-docker-mcp"]
    }
  }
}
```

## License

[MIT](LICENSE.md)
