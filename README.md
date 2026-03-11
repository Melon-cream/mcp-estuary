# mcp-estuary

`mcp-estuary` は、`mcpe.json` に定義した複数の stdio ベース MCP サーバーを、単一の HTTP endpoint `/mcp` として公開する Go 製 gateway です。

`mcp.json` と同じ記法で記述された `mcpe.json` を読み取り、自動で並列インストールして起動します。

English Version:[README-en.md](https://github.com/Melon-cream/mcp-estuary/README-en.md) 

## 主な機能

- ディレクトリの自動作成によるMCPサーバーごとの環境分離
- 削除されたMCPサーバーのディレクトリを自動削除
- `uvx`, `npx`, `docker` ランタイムに対応
- 設定ファイルの検証
- `--use` 引数による公開対象サーバーの絞り込み
- 並列インストールによる高速起動
- インストールパッケージのキャッシュ
- `/mcp` と `/healthz` エンドポイントを提供
- 各種ログ保存
- `stop`, `servers list`, `logs`, `cache clean`, `config set` CLI を提供

## 必要環境

- Go 1.26+
- 利用する MCP サーバーに応じて以下のいずれか
  - `uv`
  - `node` / `npm`
  - `docker`

## 設定

デフォルトでは、カレントディレクトリの `mcpe.json` を読み込みます。

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

補足:

- `cwd` は省略可能です。相対パスは `mcpe.json` があるディレクトリ基準で解決されます。
- 環境変数名が `_PATH` で終わる場合、値が相対パスなら `mcpe.json` 基準の絶対パスに解決されます。
- 実際のトークンや API キーは `mcpe.json` 可能な限り環境変数で投入してください。

リポジトリ直下の `mcpe.example.json` は検証用サンプルです。利用前に内容を見直してください。

## ローカル実行

```bash
go run ./cmd/mcpe serve --config mcpe.json --listen 127.0.0.1:8080
```

利用可能なコマンド:

```text
mcpe serve [--config <path>] [--listen <host:port>] [--use <name> ...] [--install-concurrency <n>]
mcpe stop
mcpe servers list [--config <path>]
mcpe logs [--server <name>]
mcpe cache clean
mcpe config set --install-concurrency <n>
mcpe --help
```

install 並列数の優先順位:

1. 環境変数 `INSTALL_CONCURRENCY`
2. `serve --install-concurrency`
3. `mcpe config set --install-concurrency`
4. デフォルト値 `2`

## HTTP Endpoint

- `POST /mcp`
- `GET /mcp`
- `DELETE /mcp`
- `GET /healthz`

`POST /mcp` では以下を扱います。

- `initialize`
- `notifications/initialized`
- `ping`
- `tools/list`
- `tools/call`

公開される tool 名は、上流 MCP サーバー間の衝突を避けるため `<server>__<tool>` 形式に変換されます。

## 状態ディレクトリ

実行時状態はデフォルトで `~/.mcp-estuary` 配下に保存されます。

- gateway log: `~/.mcp-estuary/logs/gateway.log`
- server log: `~/.mcp-estuary/logs/servers/<name>.log`
- managed workdir: `~/.mcp-estuary/mcp-servers/<name>`

保存先は `MCPE_HOME` で上書きできます。

## Docker Run

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

`/var/run/docker.sock` の mount は Docker ベース MCP サーバーを gateway から起動する場合にだけ必要です。

## Docker Compose

`compose.yaml` には以下が含まれます。

- `8080` 公開
- `mcpe.json` の mount
- `.memory` の bind mount 先
- `/root/.mcp-estuary` 用の永続 volume
- Docker ベース MCP サーバー用の `/var/run/docker.sock` mount

起動:

```bash
docker compose up --build
```

## Example

最小の Docker ベース MCP サーバー例を [examples/hello-docker-mcp/Dockerfile](https://github.com/Melon-cream/mcp-estuary/examples/hello-docker-mcp/Dockerfile) に含めています。

ビルド:

```bash
docker build -t mcpe-hello-docker-mcp ./examples/hello-docker-mcp
```

設定例: [examples/hello-docker-mcp/mcpe.json](https://github.com/Melon-cream/mcp-estuary/examples/hello-docker-mcp/mcpe.json)

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

`tools/list` と `tools/call` で `hello-docker__hello` を確認できます。

## License

[MIT](https://github.com/Melon-cream/mcp-estuary/LICENSE.md)
