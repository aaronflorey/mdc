# mdc

Run one `docker compose` command across every compose project in the current directory tree.

[![CI](https://github.com/aaronflorey/multi-docker-compose/actions/workflows/ci.yml/badge.svg)](https://github.com/aaronflorey/multi-docker-compose/actions/workflows/ci.yml)

`mdc` discovers `compose.yaml`, `compose.yml`, `docker-compose.yaml`, and `docker-compose.yml` files, picks one canonical file per directory, and executes the same compose command against each project.

## Usage

```bash
mdc [mdc flags] <docker compose args...>
```

Examples:

```bash
mdc ps
mdc up -d
mdc --depth 2 pull
mdc --jobs 4 logs --tail 50
mdc --ansi never ps
```

## Flags

- `--depth`: directory depth to scan, where `0` means the current directory only and the default is `1`
- `--jobs`: max concurrent `docker compose` processes, where `0` means all discovered targets
- `--quiet-targets`: suppress per-target section labels for non-merged output

## Behavior

- Discovery starts in the current directory and walks child directories up to the configured depth.
- If a directory contains multiple compose filenames, `mdc` uses this precedence:
  1. `compose.yaml`
  2. `compose.yml`
  3. `docker-compose.yaml`
  4. `docker-compose.yml`
- Non-`ps` commands print grouped output for each compose project in sorted directory order.
- `mdc ps` attempts `docker compose ps --format json`, merges results into one table, and falls back to stitched text output when JSON is unavailable.
- The process exits non-zero when any compose target fails.
- `mdc` parses only its own flags (`--depth`, `--jobs`, `--quiet-targets`) and passes the remaining args through to `docker compose`; `--` is only needed to force a collision like `mdc -- --version`.

## Development

```bash
task setup
task test
task test:integration
task lint
task build
task ci
```

`task run -- ps` forwards arguments to `go run .`.

`task test:integration` requires a working local Docker Engine with `docker compose` available.

## Release Automation

- `.github/workflows/build.yml` runs `release-please` on `main`
- `goreleaser` publishes tagged releases after `release-please` creates a release
- Homebrew publishing targets `aaronflorey/homebrew-tap`
- Set `HOMEBREW_TAP_GITHUB_TOKEN` in GitHub Actions secrets for tap publishing

## Git Hooks

- Hooks are managed by Lefthook via `lefthook.yml`
- `task setup` installs the pinned Lefthook version
