# Plan

## Goal

Add an optional CLI file-size limit so promptcat skips oversized files before
opening or reading them, including JSON and files selected by `auto` mode.

## Confirmed

- CLI parsing lives in `cmd/promptcat/promptcat.go` and uses a hand-written
  options parser with both separate and `--name=value` forms for existing
  options.
- Main file processing already calls `os.Stat` before opening each file, so
  size filtering can happen without changing streaming output behavior.
- Current skip diagnostics go to stderr with `Skipping (...)` reasons.
- Existing tests cover argument parsing, file expansion, output formatting, and
  streaming file reads in `cmd/promptcat/promptcat_test.go`.
- README documents CLI options and auto mode behavior.
- No promptcat-specific `AGENTS.md` exists; repository guidance comes from
  parent workspace instructions and `CONTEXT.md`. Current project tasks live
  in `mise.toml`.

## Assumptions

- New option name: `--max-size`.
- Accept human-readable decimal/binary suffixes such as `500KB`, `1MB`, and
  `1MiB`; bare values mean bytes. Case-insensitive suffixes are accepted.
- Default remains unlimited, preserving current behavior.
- A file at exactly limit is included; only files larger than limit are
  skipped.
- Invalid, zero, or negative limits fail argument parsing instead of silently
  changing behavior.
- `--max-size` applies to explicit paths, glob-expanded paths, and auto mode.

## Todo

- [x] Add `maxSize` option field and parse `--max-size VALUE` plus
      `--max-size=VALUE` with bounded unit parsing and validation.
- [x] Check `info.Size()` after directory/stat validation and before binary
      extension checks/file open; emit `Skipping (too large): <path>` to stderr.
- [x] Add focused parser and runtime tests for default behavior, suffixes,
      invalid values, exact-boundary inclusion, and oversized-file skipping.
- [x] Document `--max-size`, examples such as
      `promptcat --max-size=1MB "**/*.json"`, and its application to auto mode.
- [x] Run gofmt, `mise run test`, direct `go build`, `go vet ./...`, and
      `git diff --check`; `mise run build` remains unavailable because its
      pre-existing task invokes missing `powershell` on this Linux host.

## Implementation Order

1. Update `cmd/promptcat/promptcat.go` options, parser, usage text, and main
   processing check.
2. Update `cmd/promptcat/promptcat_test.go` with parser and file-size behavior
   coverage.
3. Update `README.md` option table, notes, and examples.
4. Run focused and project verification commands; inspect final diff.

## Flow

```text
args -> parse --max-size -> options.maxSize
file selection/expansion -> os.Stat -> directory check -> size check
  -> skip oversized file OR continue existing filters/open/stream path
```

## Risks

- Unit parsing must reject overflow instead of wrapping into a small limit.
- Files can change between `os.Stat` and `os.Open`; this is acceptable for the
  existing best-effort file processing model, but the check only guarantees
  the observed stat size.
- Human-readable suffix semantics must be documented clearly to avoid users
  confusing decimal `MB` with binary `MiB`.

## Verification

- `go test ./...` or project `mise run test` passes, including focused size-limit
  cases.
- Direct `go build -o bin/promptcat ./cmd/promptcat` passes. `mise run build`
  is blocked by pre-existing `powershell: command not found` in `mise.toml`.
- `go vet ./...` passes.
- `git diff --check` passes.
- Manual smoke test confirms an oversized JSON file is reported on stderr and
  absent from stdout while a file at the limit remains included.
