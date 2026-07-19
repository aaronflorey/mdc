# mdc

Run one `docker compose` command across every compose project in a directory tree.

[![License](https://img.shields.io/github/license/aaronflorey/mdc)](LICENSE)
[![CI](https://github.com/aaronflorey/mdc/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/aaronflorey/mdc/actions/workflows/ci.yml)
[![Latest Release](https://img.shields.io/github/v/release/aaronflorey/mdc?sort=semver)](https://github.com/aaronflorey/mdc/releases)

`mdc` discovers `compose.yaml`, `compose.yml`, `docker-compose.yaml`, and `docker-compose.yml`, picks one canonical file per directory, and runs the same `docker compose` command for each target.

## Installation

```bash
# Homebrew
brew install aaronflorey/tap/mdc

# Go
go install github.com/aaronflorey/mdc@latest
```

Prebuilt binaries are also published on the [GitHub releases page](https://github.com/aaronflorey/mdc/releases).

## Quick Start

```bash
mdc ps
mdc up -d
mdc --depth 2 pull
mdc --jobs 4 logs --tail 50
mdc --ansi never ps
```

## Usage

```bash
mdc [mdc flags] <docker compose args...>
```

## Flags

- `--depth`: scan depth (`0` = current directory only, default `1`)
- `--jobs`: max concurrent `docker compose` processes (`0` = all targets)
- `--quiet-targets`: suppress per-target section headers; merged tables, live prefixes, and completion labels retain attribution

## Setup

```bash
git clone https://github.com/aaronflorey/mdc.git
cd mdc
mise install
task setup
task ci
```

## How It Works

- Discovery starts in the current directory and walks child directories up to `--depth`.
- If a directory has multiple compose files, this precedence is used:
  1. `compose.yaml`
  2. `compose.yml`
  3. `docker-compose.yaml`
  4. `docker-compose.yml`
- `mdc` preserves the target directory basename as the Compose project name when it is unique, and switches to a deterministic unique name only when duplicate basenames would collide.
- Most non-`ps` commands print grouped output per project in sorted directory order.
- Top-level multi-target detached `mdc up -d` keeps one live status line per target on TTYs so concurrent Docker progress stays stable; non-TTY output falls back to grouped summaries, and failed targets still print grouped details. Attached multi-target `mdc up` keeps live target-prefixed output.
- Top-level multi-target `mdc logs` without follow mode (`-f` / `--follow`) uses grouped output for easier historical scanning, while explicit follow mode keeps live interleaved prefixed lines for per-target attribution.
- Top-level multi-target `mdc build` (parallel mode) suppresses noisy successful BuildKit logs and prints concise `[target] build complete` summaries; failed targets still print detailed stdout/stderr plus the standard failure summary.
- Top-level `mdc events` is serial-only for multi-target runs; if multiple targets are discovered, rerun with `--jobs 1`.
- `mdc ps` tries `docker compose ps --format json`, merges results into one table, and falls back to stitched text when JSON is unavailable.
- Top-level multi-target `mdc images` emits one merged table when all targets share a conservatively parseable table shape; otherwise it falls back to a deterministic stitched view that keeps one header and preserves all rows in sorted target order.
- Exit code is non-zero if any compose target fails.
- Ctrl-C cancels the shared run context so in-flight `docker compose` processes stop together.
- `mdc` only parses its own flags (`--depth`, `--jobs`, `--quiet-targets`) and passes everything else through to `docker compose`.

## Development

Development is driven by [mise](https://mise.jdx.dev/) for tool versions and [Task](https://taskfile.dev/) for commands. `mise install` provisions Go, GoReleaser, `hk`, `golangci-lint`, and `staticcheck` pinned to the versions in `mise.toml`.

```bash
mise install        # install pinned tools
task setup          # tidy modules and install git hooks
task test
task test:integration
task lint
task build
task ci
```

Git hooks are managed by [hk](https://github.com/jdx/hk). `task setup` runs `hk install --mise` so pre-commit lint/format and commit-msg conventional-commit checks are wired against the mise-managed tools.

`task run -- ps` forwards arguments to `go run .`.

`task test:integration` requires a working local Docker Engine with `docker compose` available.

## Releases

Releases use `release-please` for versioning and changelog management. Merged Conventional Commits are collected into a release PR, and merging that PR creates a `vX.Y.Z` tag, publishes release archives with GoReleaser, and updates the `aaronflorey/homebrew-tap` formula.

## Project Files

- [Contributing Guide](CONTRIBUTING.md)
- [Security Policy](SECURITY.md)
- [Code of Conduct](CODE_OF_CONDUCT.md)
- [MIT License](LICENSE)
