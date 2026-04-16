# Contributing

## Getting Started

```bash
git clone https://github.com/aaronflorey/mdc.git
cd mdc
task setup
task ci
```

`task test:integration` requires a working local Docker Engine with `docker compose` available.

## Development Workflow

1. Create a branch for your change.
2. Make the smallest focused change that solves the problem.
3. Run the relevant checks before opening a pull request.
4. Open a pull request with a clear description of the user-visible impact.

## Checks

```bash
task lint
task test
task test:integration
task build
```

## Commit and Release Notes

Releases are managed with `release-please`, so Conventional Commits help generate accurate release notes:

- `feat: add new behavior`
- `fix: correct existing behavior`
- `docs: update documentation`

Breaking changes should use `!` or include a `BREAKING CHANGE:` footer.
