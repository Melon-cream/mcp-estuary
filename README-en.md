# mcp-estuary

`mcp-estuary` is a Go-based gateway that exposes multiple stdio-based MCP servers defined in `mcpe.json` through a single HTTP endpoint at `/mcp`.

It reads `mcpe.json`, written in the same format as `mcp.json`, then installs and starts the configured servers automatically in parallel.

## Features

- Isolates each MCP server environment by creating dedicated directories automatically
- Automatically removes directories for MCP servers deleted from the configuration
- Supports `uvx`, `npx`, and `docker` runtimes
- Validates the configuration file
- Filters exposed servers with the `--use` argument
- Starts quickly with parallel installation
- Caches installed packages
- Exposes `/mcp` and `/healthz` endpoints
- Stores logs
- Provides `stop`, `servers list`, `logs`, `cache clean`, and `config set` CLI commands

## Requirements

- Go 1.26+
- Depending on the MCP servers you use, one or more of:
  - `uv`
  - `node` / `npm`
  - `docker`

## Configuration

By default, the gateway loads `mcpe.json` from the current directory.

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
        "TAVILY_API_KEY": "<YOUR_API_KEY>"
      }
    }
  }
}
```

Notes:

- `cwd` is optional. Relative paths are resolved from the directory containing `mcpe.json`.
- If an environment variable name ends with `_PATH`, and its value is a relative path, it is resolved relative to `mcpe.json`.
- As much as possible, inject real tokens and API keys via environment variables instead of writing them directly into `mcpe.json`.

The `mcpe.example.json` file in the repository root is a sample for verification. Review its contents before using it.

## Local Run

```bash
go run ./cmd/mcpe serve --config mcpe.json --listen 127.0.0.1:8080
```

Available commands:

```text
mcpe serve [--config <path>] [--listen <host:port>] [--use <name> ...] [--install-concurrency <n>]
mcpe stop
mcpe servers list [--config <path>]
mcpe logs [--server <name>]
mcpe cache clean
mcpe config set --install-concurrency <n>
mcpe --help
```

Install concurrency priority:

1. `INSTALL_CONCURRENCY`
2. `serve --install-concurrency`
3. `mcpe config set --install-concurrency`
4. Default value `2`

## HTTP Endpoint

- `POST /mcp`
- `GET /mcp`
- `DELETE /mcp`
- `GET /healthz`

`POST /mcp` handles:

- `initialize`
- `notifications/initialized`
- `ping`
- `tools/list`
- `tools/call`

Exposed tool names are rewritten as `<server>__<tool>` to avoid collisions across upstream MCP servers.

## State Directory

Runtime state is stored under `~/.mcp-estuary` by default.

- gateway log: `~/.mcp-estuary/logs/gateway.log`
- server log: `~/.mcp-estuary/logs/servers/<name>.log`
- managed workdir: `~/.mcp-estuary/mcp-servers/<name>`

You can override the storage location with `MCPE_HOME`.

## Docker Run

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

Mount `/var/run/docker.sock` only if the gateway needs to launch Docker-based MCP servers.

## Docker Compose

`compose.yaml` includes:

- published port `8080`
- `mcpe.json` mount
- bind mount target for `.memory`
- persistent volume for `/root/.mcp-estuary`
- `/var/run/docker.sock` mount for Docker-based MCP servers

Start:

```bash
docker compose up --build
```

## Example

A minimal Docker-based MCP server example is included in [examples/hello-docker-mcp/Dockerfile](https://github.com/Melon-cream/mcp-estuary/examples/hello-docker-mcp/Dockerfile).

Build:

```bash
docker build -t mcpe-hello-docker-mcp ./examples/hello-docker-mcp
```

Config example: [examples/hello-docker-mcp/mcpe.json](https://github.com/Melon-cream/mcp-estuary/examples/hello-docker-mcp/mcpe.json)

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

You can verify `hello-docker__hello` through `tools/list` and `tools/call`.

## License

[MIT](https://github.com/Melon-cream/mcp-estuary/LICENSE.md)
