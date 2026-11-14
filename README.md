# mdc

Run Docker Compose commands across every compose project in a directory tree, with one live progress view.

[![License](https://img.shields.io/github/license/aaronflorey/mdc)](LICENSE)
[![CI](https://github.com/aaronflorey/mdc/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/aaronflorey/mdc/actions/workflows/ci.yml)
[![Latest Release](https://img.shields.io/github/v/release/aaronflorey/mdc?sort=semver)](https://github.com/aaronflorey/mdc/releases)

`mdc` finds the compose projects below the current directory and runs a compose command against each of them concurrently. It is built on the Docker Compose library, so it does not need the `docker compose` plugin, only a reachable Docker Engine.

## Installation

```bash
# Homebrew
brew install aaronflorey/tap/mdc

# Go
go install github.com/aaronflorey/mdc@latest
```

Prebuilt binaries are also published on the [GitHub releases page](https://github.com/aaronflorey/mdc/releases).

## Quick Start

From a directory whose subdirectories each contain a compose file:

```bash
mdc ls                      # list the projects mdc discovered
mdc up -d                   # start every project in the background
mdc ps                      # one table of containers across projects
mdc --jobs 2 pull           # run at most two projects at a time
mdc logs -f web             # follow the "web" service of every project that has one
mdc down
```

Each project gets a section in the live view with one row per resource. Finished projects collapse to a single line, and a summary at the end lists projects that succeeded, were skipped, or failed. The exit code is non-zero if any project failed.

## Discovery

- `mdc` scans the current directory and its subdirectories down to `--depth` (`0` is the current directory only; the default is `1`). Unreadable directories are skipped.
- Each directory contributes at most one compose file, chosen in this order: `compose.yaml`, `compose.yml`, `docker-compose.yaml`, `docker-compose.yml`.
- The project name is the directory name, sanitized the way Compose does it. If two directories would get the same name, `mdc` gives them deterministic unique names.

## Global flags

| Flag | Default | Description |
| --- | --- | --- |
| `--depth` | `1` | Directory depth to scan for compose projects |
| `--jobs` | `0` | Maximum projects to run at once (`0` runs all at once) |
| `--profile` | | Compose profile to enable (repeatable) |
| `--env-file` | | Alternate environment file (repeatable) |
| `--parallel` | `-1` | Maximum parallelism within a project (`-1` is unlimited) |
| `--compatibility` | `false` | Run Compose in backward compatibility mode |
| `--dry-run` | `false` | Show what would happen without changing anything |
| `--all-resources` | `false` | Include resources no service uses |

Compose's `-f`, `-p`, and `--project-directory` are not offered because discovery decides them.

## Configuration

Every global flag can also be set with an environment variable or in a config file. Precedence, highest first:

1. Command-line flags
2. Environment variables named `MDC_` plus the flag name in upper case, with `-` replaced by `_` (`MDC_DEPTH`, `MDC_JOBS`, `MDC_DRY_RUN`, `MDC_ENV_FILE`)
3. The config file
4. Defaults

The config file is `mdc/config.yaml` inside your user config directory (`$XDG_CONFIG_HOME` or `~/.config` on Linux, `~/Library/Application Support` on macOS). Keys are the flag names:

```yaml
# ~/.config/mdc/config.yaml
depth: 2
jobs: 4
profile:
  - dev
```

A missing file is fine; a file that cannot be parsed is an error.

## Commands

`build`, `config`, `create`, `down`, `events`, `images`, `kill`, `logs`, `ls`, `pause`, `ps`, `pull`, `push`, `restart`, `rm`, `scale`, `start`, `stats`, `stop`, `top`, `unpause`, `up`, `version`, `volumes`, `wait`, `watch`.

Commands keep the names, arguments, and flags of their `docker compose` equivalents; run `mdc <command> --help` for the details. `--format` accepts `table` (the default) or `json`. `ls` is specific to `mdc` and lists the discovered projects.

These commands act on a single container or destination, so `mdc` does not provide them. Use `docker compose` for `attach`, `commit`, `cp`, `exec`, `export`, `port`, `run`, `publish`, `alpha`, `bridge`, `generate`, and `viz`.

### Selecting services

Commands that take `[SERVICE...]` apply the names to each project separately:

- A project that defines none of the named services is skipped.
- A name that no project defines produces a warning.
- It is an error only if no project matches any of the names.

With no service names, the whole project is used.

## Requirements

`mdc` is interactive and needs a terminal. Run it directly in one: piping or redirecting its output exits with an error. `help`, `version`, and `completion` work anywhere. Set `NO_COLOR` to disable colours.

Set `SENTRY_DSN` to report unexpected errors and panics to Sentry. Nothing is sent when it is unset.

## Development

Development is driven by [mise](https://mise.jdx.dev/) for tool versions and [Task](https://taskfile.dev/) for commands.

```bash
git clone https://github.com/aaronflorey/mdc.git
cd mdc
mise install        # install pinned tools
task setup          # tidy modules and install git hooks
task test
task lint
task build
task ci             # lint and test
```

`task run -- ps` forwards arguments to `go run .`. Git hooks are managed by [hk](https://github.com/jdx/hk); `task setup` wires pre-commit lint/format and commit-msg conventional-commit checks.

## Releases

Releases use `release-please` for versioning and changelog management. Merged Conventional Commits are collected into a release PR, and merging that PR creates a `vX.Y.Z` tag, publishes release archives with GoReleaser, and updates the `aaronflorey/homebrew-tap` formula.

## Project Files

- [Contributing Guide](CONTRIBUTING.md)
- [Security Policy](SECURITY.md)
- [Code of Conduct](CODE_OF_CONDUCT.md)
- [MIT License](LICENSE)
