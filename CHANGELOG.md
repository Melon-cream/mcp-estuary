# Changelog

All notable changes to this project will be documented in this file.

The format is based on Keep a Changelog, with release sections grouped by version and standard change categories.

## [Unreleased]

### Added

- None yet.

### Changed

- None yet.

### Fixed

- None yet.

### Removed

- None yet.

## [0.2.0] - 2026-03-13

### Added

- Added `mcpe doctor` with command checks, config loading checks, env state reporting, install checks, and `tools/list` connectivity checks.
- Added `mcpe status` with gateway runtime details, per-server state, and user systemd registration visibility.
- Added `mcpe logs -f` and `mcpe logs --follow`.
- Added `mcpe config set --systemd enable|disable`.
- Added root-level changelog files for external release tracking.

### Changed

- Changed `mcpe serve` to launch in the background by default.
- Added `${VAR}` expansion for `mcpe.json` `env` entries, using process environment variables first and `.env` next.
- Added `.env` support for `MCPE_HOME`.
- Applied `_PATH` relative path resolution after env expansion.
- Added automatic hot reload for `mcpe.json` and isolated invalid server changes from healthy servers.
- Added automatic trailing-comma repair for `mcpe.json`, with repair diffs logged to the gateway log.
- Added a `mcp-servers-logs` symlink next to `mcpe.json` that points to managed server logs.
- Added runtime status persistence under `~/.mcp-estuary/run/runtime-status.json`.
- Updated `status` so servers without env bindings are shown as `INFO`, aligned with `doctor`.
- Updated `status` so `gateway` and `systemd` use the same left-aligned section style as MCP servers.
- Updated systemd startup to stop an existing managed gateway before enabling or starting the user service.

### Fixed

- Fixed systemd unit generation so `WorkingDirectory` is written as an absolute path without invalid quoting.
- Fixed `mcpe doctor` so diagnostics run in an isolated temporary cwd even when a server config has `cwd`.
- Fixed symlink creation so an existing non-symlink `mcp-servers-logs` path is not overwritten.
- Fixed `--systemd enable` to reject transient `go run` binaries and require a stable installed executable.

### Removed

- Removed `mcpe servers list` from the public CLI.
