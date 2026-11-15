# Contributing

## Set up

You need [mise](https://mise.jdx.dev/). It installs the pinned Go version (1.26.10 or newer) and the lint tools.

```bash
git clone https://github.com/aaronflorey/mdc.git
cd mdc
mise install
mise run setup    # tidy modules and install the git hooks
```

The git hooks run linters on commit and check that commit messages follow Conventional Commits. One hook checks GitHub Action pins against the GitHub API. Export `GITHUB_TOKEN` so it isn't rate-limited, for example with `export GITHUB_TOKEN=$(gh auth token)`.

## Common tasks

| Command | What it does |
| --- | --- |
| `mise run run -- ps` | Run `mdc` from source with the given arguments. |
| `mise run test` | Run the unit tests. |
| `mise run lint` | Format, vet, and lint the code. |
| `mise run ci` | Lint and test, as CI does. |
| `mise run check` | Run every git hook check across the whole tree. |
| `mise run fix` | Apply the hooks' automatic fixes. |
| `mise run build` | Build a Linux amd64 binary into `dist/`. |
| `mise run release:snapshot` | Build a local GoReleaser snapshot. |

Interface changes are best checked by hand in a real terminal. `fixtures/manual-demo` has two small projects to run against:

```bash
cd fixtures/manual-demo
go run ../.. up -d
go run ../.. ps
go run ../.. down
```

## Making a change

1. Create a branch.
2. Make the smallest focused change that solves the problem.
3. Run `mise run ci`.
4. Open a pull request that describes the user-visible impact.

## Commits and releases

Releases are managed by [release-please](https://github.com/googleapis/release-please), which builds release notes from Conventional Commits:

- `feat: add new behaviour`
- `fix: correct existing behaviour`
- `docs: update documentation`

Mark breaking changes with `!` (for example `feat!:`) or a `BREAKING CHANGE:` footer.

Merged commits collect into a release pull request. Merging that pull request tags `vX.Y.Z`. It also publishes the release archives with GoReleaser and updates the `aaronflorey/homebrew-tap` formula.
