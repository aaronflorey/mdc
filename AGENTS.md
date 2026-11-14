# Agent Notes

`mdc` runs Docker Compose commands across every compose project found under the working directory. It imports `github.com/docker/compose/v5` as a library. It never shells out to `docker compose`.

## Layout

- `main.go`: signal context, Sentry (`SENTRY_DSN`), final error rendering, exit codes. Only this file calls `os.Exit`.
- `internal/cli`: Cobra commands, one file per command or small group. Register each one from `init()` with `Register`. Global flags are bound to a local Viper instance; never use the global one.
- `internal/cli/run.go`: `App.runEach` / `App.plan` drives every multi-project command: discover, load projects, resolve services, run, and report. Reuse it rather than writing a new loop.
- `internal/discover`: compose file discovery, file precedence, project naming.
- `internal/backend`: Docker CLI and per-target compose service construction, project loading, and `ResolveServices`.
- `internal/runner`: fan-out bounded by `--jobs`, with per-target OK/Skipped/Failed outcomes.
- `internal/ui`: the live progress view (bubbletea), tables, sections, prefixed log streams, and styles (lipgloss).

## Rules

- Interactive terminals only. Don't add non-TTY fallbacks or machine-output modes beyond the upstream `--format json` / `--json` flags.
- Mirror upstream flags (names, shorthands, defaults, hidden/deprecated) from `cmd/compose/<cmd>.go` in the pinned compose module.
- Don't add commands that target a single container or a single destination: attach, commit, cp, exec, export, port, run, publish, and the alpha commands.
- For `SERVICE` arguments, skip projects that lack every requested service. Warn about names no project defines. Error only when nothing matches anywhere.
- Never write to stdout/stderr directly while the live view runs. Compose services get discarded streams, and progress goes through `api.EventProcessor`.
- Compose v5 needs Go >= 1.26.10. Keep `go.mod` and `mise.toml` in step, and copy compose's `exclude` block when bumping compose.
- Add dependencies only when code uses them.
- Keep release automation compatible with release-please + goreleaser.
- Verify UI changes in a real PTY (e.g. `tui-test`) against `fixtures/manual-demo`. `mise run lint` and `go test ./...` must pass.
