# mdc

Run one `docker compose` command across every compose project in a directory tree.

[![CI](https://github.com/aaronflorey/mdc/actions/workflows/ci.yml/badge.svg)](https://github.com/aaronflorey/mdc/actions/workflows/ci.yml)
[![Build](https://github.com/aaronflorey/mdc/actions/workflows/build.yml/badge.svg)](https://github.com/aaronflorey/mdc/actions/workflows/build.yml)
[![Latest Release](https://img.shields.io/github/v/release/aaronflorey/mdc?label=release)](https://github.com/aaronflorey/mdc/releases)

`mdc` discovers `compose.yaml`, `compose.yml`, `docker-compose.yaml`, and `docker-compose.yml`, picks one canonical file per directory, and runs the same `docker compose` command for each target.

## Installation

Install with either Homebrew or `bin`:

```bash
# Homebrew
brew install aaronflorey/tap/mdc

# bin
bin install github.com/aaronflorey/mdc
```

`bin` project: https://github.com/aaronflorey/bin

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
- `--quiet-targets`: suppress per-target section headers for non-merged output

## How It Works

- Discovery starts in the current directory and walks child directories up to `--depth`.
- If a directory has multiple compose files, this precedence is used:
  1. `compose.yaml`
  2. `compose.yml`
  3. `docker-compose.yaml`
  4. `docker-compose.yml`
- `mdc` assigns an explicit Compose project name per target so duplicate directory basenames do not collide.
- Non-`ps` commands print grouped output per project in sorted directory order.
- `mdc ps` tries `docker compose ps --format json`, merges results into one table, and falls back to stitched text when JSON is unavailable.
- Exit code is non-zero if any compose target fails.
- `mdc` only parses its own flags (`--depth`, `--jobs`, `--quiet-targets`) and passes everything else through to `docker compose`.

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
