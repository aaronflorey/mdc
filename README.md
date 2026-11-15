# mdc

Manage a whole directory of Docker Compose projects with one command.

[![License](https://img.shields.io/github/license/aaronflorey/mdc)](LICENSE)
[![CI](https://github.com/aaronflorey/mdc/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/aaronflorey/mdc/actions/workflows/ci.yml)
[![Latest Release](https://img.shields.io/github/v/release/aaronflorey/mdc?sort=semver)](https://github.com/aaronflorey/mdc/releases)

If you keep many compose projects side by side, say one folder per service or per client, `mdc` saves you from `cd`-ing into each one. Run `mdc up -d` in the parent folder and every project starts together. You get one live view with a section per project and a progress bar per image.

```text
~/stacks
├── api/compose.yaml
├── db/compose.yaml
└── monitoring/docker-compose.yml
```

`mdc` speaks the same commands and flags as `docker compose`. It adds the things you need once there are several projects: discovery, concurrency, merged tables and prefixed logs.

## Requirements

- A running Docker Engine. `mdc` uses your current Docker context, including `DOCKER_HOST` and `DOCKER_CONTEXT`.
- An interactive terminal. `mdc` is built for people, not scripts, so it exits with an error if you pipe or redirect its output.

You don't need the `docker compose` plugin, because `mdc` includes Compose itself.

## Install

```bash
# Homebrew
brew install aaronflorey/tap/mdc

# Go 1.26.10 or newer
go install github.com/aaronflorey/mdc@latest
```

Prebuilt binaries for Linux, macOS and Windows are on the [releases page](https://github.com/aaronflorey/mdc/releases).

Check the install:

```bash
mdc version
```

## Quick start

Go to the folder that contains your projects:

```bash
cd ~/stacks
```

See which projects `mdc` found:

```bash
mdc ls -a
```

You get one row per project. Projects that have never been started show as `not created`.

Start everything in the background:

```bash
mdc up -d
```

While images are pulled, each project has its own section with a progress bar per image. As each project finishes, its section shrinks to a single line. When all are done, a summary lists which projects succeeded, were skipped and failed.

See every container in one table:

```bash
mdc ps
```

Follow the logs of every project. Each line is prefixed with `project/service`. Press Ctrl-C to stop.

```bash
mdc logs -f
```

Stop and remove everything:

```bash
mdc down
```

## Next steps

- [Usage guide](docs/usage.md): target one service across projects, control which folders are scanned, set defaults, and more.
- [Reference](docs/reference.md): discovery rules, global flags, configuration, commands, and exit codes.
- Run `mdc <command> --help` for every flag of a command.

## Project files

- [Contributing guide](CONTRIBUTING.md)
- [Security policy](SECURITY.md)
- [Code of conduct](CODE_OF_CONDUCT.md)
- [MIT license](LICENSE)
