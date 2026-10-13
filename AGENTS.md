# AGENTS.md

## Scope

These instructions apply throughout the xserial repository. More specific
`AGENTS.md` files may supplement or override local conventions, but must not
expand the user's authorization. Treat current source code, tests, and project
documentation as authoritative; do not present old notes or assumptions as facts.

## Project overview

xserial is a cross-platform serial CLI written in Go for Linux, macOS, and
Windows. It provides RawUI, a full-screen TUI, a binary workbench, YMODEM,
recording and replay, and stdio MCP access. Use `go.mod` as the source of
truth for Go and dependency versions. The CLI uses Cobra, and terminal interfaces
use Charm components.

Key directories:

- `cmd/xserial/`: application entry point.
- `internal/cmd/`: commands, argument parsing, and runtime assembly.
- `internal/serialport/`: serial enumeration, configuration, and platform support.
- `internal/backend/`: serial I/O, connection lifecycle, and reconnection.
- `internal/middleware/`: frontend/backend integration, transfers, recording, and logging.
- `internal/rawui/`, `internal/tui/`, `internal/workbench/`: terminal interaction and rendering.
- `internal/debugsession/`, `internal/mcpdaemon/`, `internal/mcpserver/`: debug history, daemon services, and MCP interfaces.
- `internal/transfer/`: YMODEM protocol and transfer writes.
- `internal/capture/`, `internal/replay/`, `internal/hexdata/`: recording/export, replay, and binary data processing.
- `docs/`, `examples/`: feature documentation and examples; `assets/`: README images.

## Development conventions

- Keep changes focused and preserve existing user changes and unrelated work.
  Analysis and review requests are read-only unless the user authorizes edits.
- Follow existing package boundaries and naming. Prefer direct implementations
  over abstractions or compatibility layers without a concrete requirement.
- Format Go changes with `gofmt`. Keep tests in the corresponding package's
  `*_test.go` files and cover observable behavior and actual regressions.
- The terminal session owns the serial connection. MCP accesses the existing
  debug session; do not introduce a competing device receive path. `xserial mcp`
  serves stdio exclusively. The shared daemon uses authenticated local IPC for
  session discovery and routing; it does not expose an HTTP MCP endpoint.
- Preserve connection-generation checks, cancellation, and shutdown semantics
  when changing I/O, reconnection, or transfers. Old requests must not write to
  a replacement connection.
- Handle platform differences through platform-specific files and build tags.
  Do not assume behavior observed on one host applies to all supported platforms.
- Update relevant documentation when user-facing commands, flags, or behavior
  change. Keep `README.md` and `README_zh.md` synchronized when editing the README.
- Do not create or maintain `CONTEXT.md` by default or rewrite unrelated documentation.

## Build and validation

Run from the repository root:

```sh
go test ./...                # Full unit test suite, as run in CI
go test -race ./...          # Race checks, as run in CI
make build                  # CGO_ENABLED=0; output goes to bin/
go run ./cmd/xserial --help  # Inspect CLI help
git diff --check            # Check whitespace errors in the diff
```

- Run affected package tests during development, then the full suite for code
  changes. Run race checks for concurrency or session lifecycle changes.
- For documentation-only changes, inspect the content and diff; Go tests are unnecessary.
- `make build` and `make release` disable CGO. On macOS, builds without CGO do not
  provide detailed USB enumeration metadata.
- `make release` clears and recreates `dist/`, producing platform archives and
  checksums. Use it only when the task requires release artifacts.
- Keep generated files in `bin/` and `dist/`, and local runtime data, out of source changes.
- Unit tests, builds, and simulated ports do not establish real-device transfer,
  unplug/reconnect, or terminal rendering correctness. Report the actual validation scope.

## Git workflow

- Determine commit message conventions only when the user requests a commit or
  asks for a commit message. Follow explicit project conventions first, otherwise
  inspect recent non-merge subjects. If the style is unclear, use Conventional
  Commits in the form `type(scope): description`.
- Inspect the actual diff before preparing a commit message. Before committing,
  inspect the staged changes and exclude unrelated work.

## Commit attribution

- When preparing a commit message for changes that Codex contributed to, append
  the following trailer after a blank line:

  ```text
  Co-authored-by: Codex <noreply@openai.com>
  ```

- Preserve the human author's Git identity and any existing co-author trailers.
- Do not attribute changes to Codex when it did not contribute to them.
- This attribution rule does not authorize committing, pushing, or rewriting
  existing commits; perform those actions only when explicitly requested.
