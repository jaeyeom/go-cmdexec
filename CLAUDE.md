# go-cmdexec

## Build / Test / Run

- `make all` for local dev (formats, fixes, then validates)
- `make check` for CI-equivalent read-only checks
- Formatter is **gofumpt**, not gofmt — `gofumpt -w .`
- Markdown is formatted with **prettier** — install via npm, not a Go tool
- **semgrep** is required for `make check` — install via `pip install semgrep`

## Architecture

- Single-package library (`package cmdexec`) — all `.go` files live at the repo root, no subdirectories
- **Strict dependency policy**: non-test code may only import stdlib and `golang.org/x/sys`. This is enforced by depguard but exists because this is a low-level library — adding transitive deps would burden consumers
- Execute error contract is intentionally split: transport/system errors return `(nil, error)`, process exits return `(*ExecutionResult, nil)` — do not conflate the two paths

## Conventions

- **No testify** — use stdlib `testing` only (enforced by depguard)
- **No `log` package** — use `log/slog` (enforced by depguard)
- **No `syscall`** — use `golang.org/x/sys` instead
- JSON tags use camelCase, YAML tags use snake_case (enforced by tagliatelle)

## Gotchas

- `make fix` depends on `make format` to run first — running them concurrently causes file write races
- `Stdin` + `MaxRetries > 0` without `StdinFactory` is a validation error, not a silent bug — the reader is consumed on first attempt
