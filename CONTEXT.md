# CONTEXT.md

## Purpose
- Fast boot summary for `apps/promptcat/`.

## Current Shape
- Root `install.sh` installs the current checkout with `go install ./cmd/promptcat`, honoring Go's normal installation paths and working from any caller directory. It requires Bash and Go, not mise or Bun.
- `clipboard [folder]` recursively selects source files with the archive filter, then copies normal formatted text. Explicit files/globs can be mixed with folders and retain their existing selection behavior. `cmd/promptcat/clipboard_test.go` tests clipboard capture without changing the real clipboard.
- `pack [folder]` is an alias for `archive --chatgpt`, with the same ZIP defaults, selection rules, validation, and clipboard support. The default output remains `archive.zip`; an existing output directory uses `<selected-folder>.zip`, relative outputs resolve inside the selected folder, and successful ZIP exports replace existing files only after writing completes. ZIP exports show one file-count progress bar only when stdout and stderr are terminals, followed by a stderr summary of files, skipped paths, size, duration and output. `NO_COLOR` disables colors; `--verbose` lists paths. `pack_test.go` and `pack_progress_test.go` cover command and progress behavior.
- Top-level app root with project files and local instructions.
- Positional `!glob` arguments exclude expanded file paths; `--exclude` remains extension-only.
- Project workflows use `mise.toml`; multi-step build and release tasks run through `scripts/*.mjs` with ZX.
- Symlinks and non-regular files are skipped; auto mode ignores Python environments and cache directories including `.nox`.
- Directory walks avoid per-directory sorting, simple `**.ext` globs use extension dispatch, final glob results remain deterministic, and file output uses safe serial streaming with reusable buffers.
- All archive formats share ordered file selection: prune excluded directories and symlinks, apply Git ignore rules, reject sensitive paths and key content, media, oversized files and binary/non-UTF-8 content. `--explain` reports the first rejection; ZIP inventories also record skips. Repositories require Git; standalone folders use a Git-compatible matcher for nested `.gitignore` files, starting at the selected root. Rules are best-effort and do not redact secrets from source or patches. `--pattern` restricts candidate names and `--include` adds patterns without bypassing filters. `archive` formats selected text in deterministic `archive.tar.zst`; `--files` stores separate entries, `--clipboard` copies the archive, and `--max-size`, `--ignore-dir`, `--exclude-repo`, and `--output` control selection and output (including `~/` home paths).
- `archive --ai` adds `.promptcat/project.json`, `AI_INSTRUCTIONS.md`, the embedded Python navigator, and Git patches under `.promptcat/patches/` (`root/` for the selected repository, `repo/<path>/` for nested repositories). The navigator lazily scans either `export.txt` or separately archived files for symbols, references, callers, tests, and impact. Project metadata includes a `repositories` array with archive-relative paths and per-repository Git state for the selected folder, including nested repositories and linked worktrees; legacy top-level `git` and `changes` fields remain. Patches contain per-commit changes ahead of `origin/HEAD` (upstream fallback) and staged/unstaged tracked-file changes, filtered to archived files only.
- `archive --chatgpt` is an opt-in ZIP preset (`archive.zip`, requiring a `.zip` output) that implies `--ai --files` without changing legacy TAR/Zstandard defaults. It preserves source bytes, streams validated in-memory file buffers into a temporary ZIP (staging only generated metadata), records a per-file integrity/selection inventory in `.promptcat/files.json`, adds `START_HERE.md`, and writes ZIPs without external archivers. Indexed navigator reads/listings are selective, with plain-text search dropping scanned source from memory; Git repository discovery and metadata are reused within each export; unborn Git HEADs produce no patches; `files`/`map` paginate with `--offset`, and source output defaults to 80 lines / 16,000 characters with explicit continuation notices. Python navigator regression tests run from the Go test suite when Python 3 is available.
- `update` fetches the latest GitHub release, selects the current platform's asset, verifies its SHA-256 checksum, and replaces the installed executable; `--upgrade` remains a legacy alias.
- `autocomplete [install] [shell]` prints or installs Bash, Fish, Zsh, and PowerShell completion; installation uses per-user shell completion paths and preserves existing PowerShell profile content.
- `clipboard` runs the normal formatter and copies its output using the first available platform clipboard backend; with no inputs it uses archive defaults, exclusions, and size limits.
- `default.pgo` contains the representative small-file streaming profile used by `mise run build`.

## Verification
- Use the folder's mise.toml or README commands when present.

- All archive types skip symlinks; no archive follows external targets.
