Plan for the implementation:
- Language: Go
- Scope: fresh CLI in this empty repo
- Default discovery: support both compose.y*ml and docker-compose.y*ml
Design
- Build a single lightweight binary, no external CLI framework.
- Default command shape: mdc <docker-compose-args...>
- Discovery scans:
  - current directory
  - immediate child directories only
- Per directory, select one canonical compose file to avoid double-running the same project.
  - Suggested precedence: compose.yaml, compose.yml, docker-compose.yaml, docker-compose.yml
Execution Model
- For each discovered target, run:
  - docker compose -f <file> --project-directory <dir> <args...>
- Run all targets in parallel with goroutines.
- Keep output deterministic by collecting per-target results and printing in sorted order.
- Exit code:
  - 0 if all targets succeed
  - non-zero if any target fails, with a short failure summary
Smart Output Stitching
- Special-case ps:
  - call docker compose ps --format json
  - parse and merge all rows
  - dedupe by stable key, preferably container ID, with fallback to name
  - render one combined table with one header
- Fallback behavior:
  - if json output is unavailable or parsing fails, fall back to line-based stitching and header dedupe
- For other commands:
  - print grouped output per stack with a short label like [services/api]
  - dedupe exact duplicate lines only when that is clearly safe
Minimal Feature Set
- v1 defaults:
  - discovery depth 1
  - parallel execution enabled
  - smart ps merge
  - passthrough for all other docker compose args
- Small optional flags worth adding now:
  - --depth default 1
  - --jobs default 0 meaning unbounded/all discovered
  - --quiet-targets to suppress section labels for merged output modes
Implementation Layout
- go.mod
- main.go
- main_test.go
Tests
- discovery finds only supported files at depth 1
- duplicate filenames in one directory choose one target
- ps JSON merge produces a single header
- dedupe removes repeated table headers
- passthrough args are preserved
- aggregated exit code is correct when one target fails
Important Edge Case
- If both compose.yml and docker-compose.yml exist in the same directory, I would treat that as one project and pick one file by precedence, not run both.
Assumptions
- I’d default the binary name to mdc
- I’d keep dependencies at zero and use only the Go standard library
