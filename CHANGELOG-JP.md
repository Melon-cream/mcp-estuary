# CHANGELOG

## [0.2.0] - 2026-03-13

### Added

- `mcpe doctor` を追加
  - 必要コマンド確認
  - 設定読込確認
  - env 状態表示
  - install 確認
  - `tools/list` 疎通確認
- `mcpe status` を追加
  - gateway 実行状態表示
  - server ごとの状態表示
  - user systemd 登録状態表示
- `mcpe logs -f` / `mcpe logs --follow` を追加
- `mcpe config set --systemd enable|disable` を追加
- 外向け changelog としてルートに `CHANGELOG.md` / `CHANGELOG-JP.md` を追加

### Changed

- `mcpe serve` はデフォルトでバックグラウンド起動するよう変更
- `mcpe.json` の `env` で `${VAR}` 展開に対応
  - 親プロセス環境変数を優先
  - 未設定時は `.env` を参照
- `MCPE_HOME` も `.env` から読めるよう変更
- `_PATH` 相対パス解決を env 展開後に適用するよう変更
- `mcpe.json` のホットリロードに対応し、不正な server 変更を正常 server から分離するよう変更
- `mcpe.json` の trailing comma を自動修復し、gateway log に diff を出すよう変更
- `mcpe.json` の隣に `mcp-servers-logs` symlink を作成するよう変更
- runtime status を `~/.mcp-estuary/run/runtime-status.json` に保存するよう変更
- `status` でも env 未設定 server を `INFO` 表示に変更
- `status` の `gateway` / `systemd` を MCP server と同じ左寄せセクション表示に変更
- systemd 起動前に既存の管理対象 gateway を停止するよう変更

### Fixed

- systemd unit 生成で `WorkingDirectory` に不正な引用符が入る問題を修正
- `mcpe doctor` が `cwd` 指定 server の実ディレクトリを汚し得る問題を修正
- `mcp-servers-logs` 既存パスを無条件に上書きしないよう修正
- `--systemd enable` で `go run` の一時バイナリを拒否するよう修正

### Removed

- 公開 CLI から `mcpe servers list` を削除
