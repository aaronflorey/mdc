# Reference

## Discovery

`mdc` scans the current directory and its subdirectories down to `--depth` levels.

- `--depth 0` scans the current directory only. The default, `1`, also scans its direct subdirectories.
- Directories that can't be read are skipped.
- Each directory provides at most one compose file. The first one present in this order is used:
  1. `compose.yaml`
  2. `compose.yml`
  3. `docker-compose.yaml`
  4. `docker-compose.yml`
- The project name is the directory name, cleaned up the way Compose does it. If two directories end up with the same name, `mdc` makes them unique, and the names stay the same between runs.
- `mdc` exits with an error if it finds no compose files.

## Global flags

These flags work with every command.

| Flag | Default | Description |
| --- | --- | --- |
| `--depth` | `1` | Directory depth to scan for compose projects. |
| `--jobs` | `0` | Maximum number of projects to run at once. `0` runs them all at once. |
| `--profile` | none | Compose profile to enable. Repeatable. |
| `--env-file` | none | Alternate environment file. Repeatable. |
| `--parallel` | `-1` | Maximum parallel operations within one project. `-1` means no limit. |
| `--compatibility` | `false` | Run Compose in backward compatibility mode. |
| `--dry-run` | `false` | Show what would happen without changing anything. |
| `--all-resources` | `false` | Include resources that no service uses. |

`mdc` doesn't offer `-f`, `-p`, `--project-directory`, `--progress` or `--ansi`, because discovery and the built-in display take care of those.

## Configuration

Global flags can be set in three places. Highest priority first:

1. Command-line flags.
2. Environment variables: `MDC_` followed by the flag name in upper case, with `-` replaced by `_`. For example `MDC_DEPTH`, `MDC_JOBS`, `MDC_DRY_RUN` and `MDC_ENV_FILE`.
3. The config file.

Anything not set in one of these uses the default.

The config file is `mdc/config.yaml` in your user configuration directory:

| Platform | Path |
| --- | --- |
| Linux | `$XDG_CONFIG_HOME/mdc/config.yaml`, or `~/.config/mdc/config.yaml` |
| macOS | `~/Library/Application Support/mdc/config.yaml` |
| Windows | `%AppData%\mdc\config.yaml` |

Keys are the flag names:

```yaml
depth: 2
jobs: 4
parallel: 2
profile:
  - dev
env-file:
  - .env.local
```

A missing file is ignored. A file that can't be parsed is an error.

## Other environment variables

| Variable | Effect |
| --- | --- |
| `DOCKER_HOST`, `DOCKER_CONTEXT` | Choose the Docker Engine, as with the `docker` CLI. |
| `NO_COLOR` | Turn off colour. |
| `SENTRY_DSN` | Send unexpected errors and crashes to this Sentry DSN. Nothing is sent when it is unset. |

## Commands

Commands use the same names, arguments and flags as their `docker compose` counterparts. Run `mdc <command> --help` for the full flag list.

| Command | Description |
| --- | --- |
| `build` | Build or rebuild service images. |
| `config` | Print each project's resolved configuration. |
| `create` | Create containers without starting them. |
| `down` | Stop and remove containers and networks. |
| `events` | Stream container events from every project. |
| `images` | List images used by the created containers. |
| `kill` | Force service containers to stop. |
| `logs` | Show container output, prefixed with `project/service`. |
| `ls` | List the discovered projects and their status. Add `-a` to include stopped and never-started projects. |
| `pause`, `unpause` | Pause or resume services. |
| `ps` | List containers in one table. |
| `pull`, `push` | Pull or push service images, with one progress bar per image. |
| `restart`, `start`, `stop` | Restart, start or stop services. |
| `rm` | Remove stopped containers. Asks once for confirmation unless `-f` is given. |
| `scale` | Set the number of replicas for services. |
| `stats` | Live resource usage table. Press `q` to quit. Add `--no-stream` for a single snapshot. |
| `top` | Show running processes, one section per project. |
| `up` | Create and start containers. Add `-d` to run them in the background. |
| `version` | Show the `mdc` version and the Compose library version. |
| `volumes` | List volumes. |
| `wait` | Wait until service containers stop, and report their exit codes. |
| `watch` | Rebuild or sync services when their files change. |
| `completion` | Generate a shell completion script. |

### Service arguments

Commands that take `[SERVICE...]` apply the names to each project separately:

- A project that defines none of the names is skipped.
- A name that no project defines produces a single warning.
- The command fails only if no project matches any of the names.
- With no names, the whole project is used.

### Output formats

`--format` accepts `table` (the default) or `json`. In JSON, `ps`, `images`, `volumes` and `ls` print one array that covers every project, and `stats` prints one object per line.

### Differences from `docker compose`

- `--abort-on-container-exit`, `--abort-on-container-failure` and `--exit-code-from` act on each project separately.
- `--attach`, `--exit-code-from` and `--scale` ignore service names that a project doesn't define.
- `config --hash` isn't supported.
- `config --no-interpolate` can't be combined with `--resolve-image-digests` or `--lock-image-digests`.
- `config -o` writes the file inside each project's directory, so the path must be relative.
- `up --menu` is accepted but has no effect.

### Commands not included

These commands act on a single container or a single destination, so they don't fit a multi-project tool. Use `docker compose` in the project's directory instead:

`attach`, `commit`, `cp`, `exec`, `export`, `port`, `run`, `publish`, `alpha`, `bridge`, `generate`, `viz`.

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | Every project succeeded or was skipped. |
| `1` | At least one project failed, or the command could not run. |
| `2` | `mdc` hit an internal error. |
| `130` | Cancelled with Ctrl-C. |
| other | With `up --exit-code-from`, the exit code of that service's container. |
