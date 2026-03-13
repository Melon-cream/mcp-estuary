# CHANGELOG v0.2.0

## Added

- `mcpe doctor` を追加
  - 必要コマンドの存在確認
  - `mcpe.json` / `.env` の読込結果確認
  - server ごとの `env` 設定状態表示
  - install 実行結果確認
  - `tools/list` 疎通確認
- `mcpe status` を追加
  - gateway の PID / listen address / started_at 表示
  - server ごとの command / args / cwd / env 状態表示
  - install 状態、last_start_at、last_error 表示
  - user systemd 上の `mcpe.service` の登録状態表示
- `mcpe logs -f` / `mcpe logs --follow` を追加
- `mcpe config set --systemd enable|disable` を追加
  - `go run` の一時バイナリではなく、安定した `mcpe` 実行ファイルのみ service 化対象にする
  - enable 時に user systemd 登録と `systemctl --user status mcpe.service` の案内を表示

## Changed

- `mcpe serve` はデフォルトでバックグラウンド起動するよう変更
- `mcpe.json` の `env` で `${VAR}` 展開に対応
  - 親プロセス環境変数を優先
  - 未設定時は `mcpe.json` と同じディレクトリの `.env` を参照
  - 未定義参照は当該 server の設定エラーとして扱う
- `_PATH` 相対パス解決は環境変数展開後に適用するよう変更
- `MCPE_HOME` も `.env` から読めるよう変更
- `mcpe.json` の trailing comma を自動修復し、gateway log に差分を出すよう変更
- `mcpe serve` 実行中の設定ホットリロードを追加
  - 不正な server があっても有効な server は継続
- `mcpe.json` の隣に `mcp-servers-logs` シンボリックリンクを自動生成するよう変更
  - 既存の非 symlink ファイルは上書きせずエラー扱いにする
- runtime status を `~/.mcp-estuary/run/runtime-status.json` に保存するよう変更
- `mcpe doctor` は `cwd` 指定 server でも隔離した一時ディレクトリ上で診断するよう変更
- `env` 未設定 server は `doctor` 上で `WARN` ではなく `INFO` 扱いに変更
- `status` でも `env` 未設定 server は `INFO` 扱いに変更
- `status` の `gateway` / `systemd` 表示を server と同じ左寄せレイアウトに変更
- systemd 起動前に既存の管理対象 gateway を停止するよう変更
- `mcpe servers list` を削除

## Documentation

- `README.md` を英語版へ再構成
- `README-JP.md` を追加
- quick startup、機能対応表、Claude/Codex 設定例、Docker 環境の env 注意点を追記

## Tests

- `.env` 展開、未定義 env エラー、JSON 自動修復の設定テストを追加
- `.env` からの `MCPE_HOME` 解決テストを追加
