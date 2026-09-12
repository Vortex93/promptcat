# CONTEXT.md

## Purpose
- Fast boot summary for `apps/promptcat/`.

## Current Shape
- Top-level app root with project files and local instructions.
- Positional `!glob` arguments exclude expanded file paths; `--exclude` remains extension-only.
- Project workflows use `mise.toml`; multi-step build and release tasks run through `scripts/*.mjs` with ZX.
- Symlinks and non-regular files are skipped; auto mode ignores Python environments and cache directories including `.nox`.
- Directory walks avoid per-directory sorting, simple `**.ext` globs use extension dispatch, final glob results remain deterministic, and file output uses safe serial streaming with reusable buffers.
- `archive` runs the normal Promptcat formatter over files matching the supported language, web, configuration, infrastructure, and shell extensions in `defaultArchivePatterns`, then stores the resulting `export.txt` in a deterministic `archive.tar.zst` using Zstandard level 3; `--files` stores matching files as separate archive entries, and `--clipboard` also copies the archive file as a file object. Directories in `defaultArchiveIgnoredDirs` are skipped by default, files over the default 1 MiB limit are skipped, `--max-size` changes that limit, `--ignore-dir` adds more exclusions, `--include` adds patterns, `--pattern` replaces them, and `--output` changes the destination.
- `autocomplete [install] [shell]` prints or installs Bash, Fish, Zsh, and PowerShell completion; installation uses per-user shell completion paths and preserves existing PowerShell profile content.
- `clipboard` runs the normal formatter and copies its output using the first available platform clipboard backend; with no inputs it uses archive defaults, exclusions, and size limits.
- `default.pgo` contains the representative small-file streaming profile used by `mise run build`.

## Verification
- Use the folder's mise.toml or README commands when present.
