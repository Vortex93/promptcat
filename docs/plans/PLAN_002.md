# Plan

## Goal

Fix high-impact findings 001–003: prevent symlink-based file disclosure,
return non-zero on output failures, and keep Python virtual environments and
cache directories out of `promptcat auto`.

## Confirmed

- Explicit files are currently inspected with `os.Stat` and opened with
  `os.Open`, both of which follow symlinks.
- Glob and auto traversal use `filepath.WalkDir`; symlink entries are treated as
  ordinary files, while symlink directories are not followed by `WalkDir`.
- `writeFileBlockFromFile` returns output errors, but `main` treats every
  non-binary error as a skippable read error and continues.
- `main` creates `bufio.NewWriter(os.Stdout)` and defers `Flush()` without
  checking its error.
- Auto directory filtering is centralized in `autoIgnoredDirs` and
  `isAutoIgnoredDir`; matching is already case-insensitive.
- Existing tests are split between `cmd/promptcat/promptcat_test.go` and
  `cmd/promptcat/auto_test.go`.
- `--max-size` work and workflow migration are present in the working tree;
  this plan targets only findings 001–003.

## Assumptions

- Symlinks are rejected by default for explicit paths, glob matches, and auto
  traversal. No follow-within-root mode is added; rejection is the smallest
  safe contract.
- Symlink skips use a clear stderr reason such as
  `Skipping (symlink): <path>` and do not affect successful processing of other
  inputs.
- Output errors are fatal: processing returns the original error (wrapped with
  operation/path context where useful), flush errors are checked, and the CLI
  exits non-zero.
- Existing not-found, directory, binary, and ordinary read-skip behavior stays
  unchanged unless the failure is an output/write/flush error.
- Auto mode adds `.venv`, `venv`, `__pycache__`, `.pytest_cache`, `.mypy_cache`,
  `.ruff_cache`, `.tox`, and `.pixi` to its default ignored directory set.
- Findings 004–006 remain out of scope for this change.

## Todo

- [x] Add symlink detection to `WalkDir` callbacks in `expandInputs` and
      `selectAutoFiles`, and use `os.Lstat` for explicit input checks so
      symlink files cannot escape the selected project.
- [x] Refactor CLI execution into an error-returning `run` path; distinguish
      binary-content skips from fatal output errors and explicitly propagate
      `bufio.Writer.Flush()` failures through `main`.
- [x] Extend `autoIgnoredDirs` with Python environment/cache directory names.
- [x] Add regression tests for explicit/glob/auto symlinks, failing writers and
      flushes, and each Python directory exclusion.
- [x] Update README/CONTEXT only where user-visible behavior or durable project
      workflow needs documentation.
- [x] Run gofmt, `mise run setup`, `mise run test`, `mise run build`,
      `go vet ./...`, and `git diff --check`; separately smoke-test exit status
      and stderr for symlink and output-failure cases. `aft_inspect` was
      attempted but remains blocked by its pre-existing `callgraph_unavailable`
      failure; Go tests, vet, and build passed.

## Implementation Order

1. Update `cmd/promptcat/promptcat.go` symlink checks and fatal output-error
   flow.
2. Update `cmd/promptcat/auto.go` default ignored directories and
   `cmd/promptcat/auto_test.go` traversal coverage.
3. Update `cmd/promptcat/promptcat_test.go` writer/flush and explicit/glob
   symlink coverage.
4. Update README or CONTEXT only if implementation wording requires it.
5. Run full verification and independently review security/error paths.

## Flow

```text
input -> Lstat/WalkDir entry type check -> reject symlink or continue
  -> existing filters -> open/read/stream
  -> return output error OR flush error -> main prints error and exits 1

auto WalkDir -> case-insensitive default/custom ignored-dir check
  -> SkipDir for Python env/cache directory -> select remaining files
```

## Risks

- Using `os.Stat` anywhere on explicit input before symlink rejection would
  preserve the disclosure bug; `Lstat` must be the first metadata check.
- A buffered writer can delay broken-pipe or disk-full errors until flush, so
  both per-write returns and final flush must be checked.
- Changing all write errors to fatal could alter behavior for input read errors;
  classify only errors originating from output operations or preserve a clear
  error boundary in the processing function.
- Rejecting symlinks is a deliberate compatibility change for users who expect
  linked source files; it prioritizes preventing unintended outside-file reads.

## Verification

- Unit tests prove symlinks are absent from explicit, glob, and auto results.
- Unit tests prove output writer and final flush failures return errors.
- Auto tests prove every listed Python environment/cache directory is skipped,
  including case variants.
- `mise run test` passes.
- `mise run build` passes; if environment-specific task tooling blocks it, run
  direct `go build` and record the pre-existing blocker rather than weakening
  the check.
- `go vet ./...` and `git diff --check` pass.
- Manual subprocess smoke tests confirm failures exit non-zero and do not claim
  successful completion.
