# Usage guide

Common tasks with `mdc`. Every example runs in the folder that contains your compose projects. For exact flag lists, see the [reference](reference.md) or `mdc <command> --help`.

## Work with one service across projects

Pass service names the same way you would to `docker compose`:

```bash
mdc restart web
```

Each project restarts its `web` service. Projects without a `web` service are skipped, and the summary lists them as `no matching services`. This makes it safe to name a service that only some projects have.

If you name a service that no project defines, `mdc` prints one warning and carries on with the names that did match:

```text
Warning: no project defines wbe
```

The command fails only when none of the names match any project.

## Choose which folders are scanned

By default `mdc` looks in the current folder and its direct subfolders. To include deeper folders, raise `--depth`:

```bash
mdc --depth 2 ps
```

Use `--depth 0` to act on the current folder only.

Check what was found before you run anything that changes state:

```bash
mdc --depth 2 ls -a
```

When a folder contains more than one compose file, `mdc` uses only one of them. The [reference](reference.md#discovery) lists the order it checks them in.

## Limit how many projects run at once

All projects run concurrently by default. To ease the load on a slow machine or a rate-limited registry, use `--jobs`:

```bash
mdc --jobs 2 pull
```

`logs -f`, `events` and other streaming commands always follow every project at once, whatever `--jobs` is set to.

## Pull, build and push images

```bash
mdc pull
mdc build
mdc up -d --build
```

Each project gets its own section, with a progress bar per image. If a build fails, the summary shows the end of the build output so you can see what went wrong without scrolling.

## Check what is running

```bash
mdc ps            # running containers across all projects
mdc ps -a         # include stopped containers
mdc images        # images used by the containers
mdc top           # processes, one section per project
mdc stats         # live resource usage; press q to quit
```

Tables have a `PROJECT` column and fit your terminal width. Less important columns are dropped first when the terminal is narrow.

## Follow logs

```bash
mdc logs -f              # every service in every project
mdc logs -f web worker   # only these services, wherever they exist
mdc logs --tail 50       # the last 50 lines of each container
```

Each line starts with `project/service │`, and every service keeps the same colour for the whole session.

## Run in the foreground

Without `-d`, `mdc up` starts every project and then streams their combined logs:

```bash
mdc up
```

Press Ctrl-C once to stop the containers gracefully. Press it again to force them to stop.

`--abort-on-container-exit` and `--exit-code-from` act on each project separately. When a container exits, only its own project is stopped.

## Use profiles and environment files

These work as they do in `docker compose`, and apply to every project:

```bash
mdc --profile debug up -d
mdc --env-file .env.local up -d
```

Both flags can be repeated.

## Set your own defaults

Put the flags you always use in a config file instead of typing them each time. On Linux, create `~/.config/mdc/config.yaml`:

```yaml
depth: 2
jobs: 4
profile:
  - dev
```

You can also use environment variables, such as `MDC_DEPTH=2`. Flags on the command line take priority over both. The [reference](reference.md#configuration) gives the file location on each platform and the full precedence order.

## Remove stopped containers

```bash
mdc rm
```

`mdc` first lists the stopped containers in every project and asks once for confirmation. Add `-f` to skip the question.

## Report errors to Sentry

Set `SENTRY_DSN` to send unexpected errors and crashes to your Sentry project:

```bash
export SENTRY_DSN=https://<key>@<host>/<project-id>
```

Nothing is sent when it is unset.

## When to use `docker compose` instead

Commands that act on one container, such as `exec`, `run`, `cp` and `attach`, aren't part of `mdc`. Change into the project's folder and use `docker compose` for those. The [reference](reference.md#commands-not-included) has the full list.
