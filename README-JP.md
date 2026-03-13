# mcp-estuary

`mcp-estuary` は、複数の stdio ベース MCP サーバーを単一の streamable HTTP `/mcp` endpoint として公開する minimal MCP gateway です。

English README: [README.md](https://github.com/Melon-cream/mcp-estuary/blob/main/README.md)

## Quick Startup

まず `mcp.json` と同じ形で `mcpe.json` を書きます。

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

次に gateway を起動します。

```bash
mcpe serve --config mcpe.json --listen 127.0.0.1:8080
```

クライアント側では `mcp-estuary` を 1 つの MCP server として設定します。

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

あとは利用するだけです。

Claude から使う設定例:

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

Codex `config.toml` の設定例:

```toml
[mcp_servers.estuary]
url = "http://127.0.0.1:8080/mcp"
```

CLI から追加する場合:

```bash
codex mcp add estuary --url http://127.0.0.1:8080/mcp
```

## 機能対応表

| 機能 | 対応 |
| --- | --- |
| 単一 `/mcp` streamable HTTP gateway | ☑ |
| `uvx` / `npx` / `docker` ランタイム | ☑ |
| `mcpe.json` の `.env` 展開 | ☑ |
| 展開後の `_PATH` 相対パス解決 | ☑ |
| trailing comma の自動修復 | ☑ |
| デフォルトのバックグラウンド起動 | ☑ |
| `mcpe config set --systemd enable|disable` | ☑ |
| 設定ホットリロード | ☑ |
| `mcpe doctor` | ☑ |
| `mcpe status` | ☑ |
| `mcpe logs -f` / `--follow` | ☑ |

## Requirements

- Go 1.26+
- 利用するサーバーに応じて以下のいずれか
  - `uv`
  - `node` / `npm`
  - `docker`

## 設定仕様

デフォルトではカレントディレクトリの `mcpe.json` を読み込みます。

環境変数まわり:

- `env` 内の `${VAR}` は、まず親プロセス環境変数、次に `mcpe.json` と同じディレクトリの `.env` から展開します。
- 未定義の `${VAR}` を参照した server は不正扱いになり、その server だけスキップされます。
- `_PATH` で終わる環境変数は、展開後の値が相対パスなら `mcpe.json` 基準の絶対パスに変換します。
- `MCPE_HOME` も同じ `.env` から読めます。親プロセス側で設定されていればそちらを優先します。
- Docker runtime の MCP server にはホスト側の環境変数は自動注入されません。必要な値は `docker run` 側で明示してください。

自動修復:

- `mcpe.json` の trailing comma は自動修復されます。
- 修復差分は gateway log に出ます。

ホットリロード:

- `mcpe serve` 実行中は `mcpe.json` を定期監視して自動再読込します。
- 設定不備がある server が混ざっても、正常な server はそのまま継続します。

server ライフサイクル:

- gateway 起動だけでは upstream MCP server は常駐起動しません。
- `tools/list` は tool metadata の発見のために upstream server を短時間だけ起動し、直後に停止する場合があります。
- `tools/call` は対象 upstream server だけを起動し、アイドル timeout 後に停止します。

## CLI

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

install 並列数の優先順位:

1. `INSTALL_CONCURRENCY`
2. `serve --install-concurrency`
3. `mcpe config set --install-concurrency`
4. デフォルト `2`

## 状態ディレクトリ

実行時状態はデフォルトで `~/.mcp-estuary` 配下に保存されます。

- gateway log: `~/.mcp-estuary/logs/gateway.log`
- server log: `~/.mcp-estuary/logs/servers/<name>.log`
- managed workdir: `~/.mcp-estuary/mcp-servers/<name>`
- runtime status: `~/.mcp-estuary/run/runtime-status.json`

起動時には `mcpe.json` の隣に `mcp-servers-logs` シンボリックリンクも自動作成されます。

`mcpe status` では、user systemd 上の `mcpe.service` の登録・enable・active 状態も確認できます。

## HTTP Endpoint

- `POST /mcp`
- `GET /mcp`
- `DELETE /mcp`
- `GET /healthz`

MCP client では必ず `/mcp` を含む URL を指定してください。`http://127.0.0.1:8080` や `http://127.0.0.1:8080/mcp/` に向けると `404 page not found` が返ります。

`POST /mcp` では次を扱います。

- `initialize`
- `notifications/initialized`
- `ping`
- `tools/list`
- `tools/call`

公開名は `<server>__<tool>` に変換されます。

## Docker

ビルド:

```bash
docker build -t mcp-estuary .
```

実行:

```bash
docker run --rm -p 8080:8080 \
  -v "$(pwd)/mcpe.json:/workspace/mcpe.json:ro" \
  -v "$(pwd)/.memory:/workspace/.memory" \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v mcp-estuary-data:/root/.mcp-estuary \
  mcp-estuary
```

`/var/run/docker.sock` は Docker ベース MCP server を起動する場合のみ必要です。

## Docker Compose

`compose.yaml` には以下が含まれます。

- `8080` 公開
- `mcpe.json` mount
- `.memory` bind mount
- `/root/.mcp-estuary` 永続 volume
- Docker ベース MCP server 用 `/var/run/docker.sock`

起動:

```bash
docker compose up --build
```

## Example

最小の Docker MCP 例:

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
