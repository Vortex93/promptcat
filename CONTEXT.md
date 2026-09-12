# CONTEXT.md

## Purpose
- Fast boot summary for `apps/promptcat/`.

## Current Shape
- Top-level app root with project files and local instructions.
- Positional `!glob` arguments exclude expanded file paths; `--exclude` remains extension-only.
- Project workflows use `mise.toml`; multi-step build and release tasks run through `scripts/*.mjs` with ZX.
- Symlinks and non-regular files are skipped; auto mode ignores Python environments and cache directories including `.nox`.
- Directory walks avoid per-directory sorting, simple `**.ext` globs use extension dispatch, final glob results remain deterministic, and file output uses safe serial streaming with reusable buffers.
- `archive` creates a deterministic `archive.tar.zst` from the supported language, web, configuration, infrastructure, and shell extensions in `defaultArchivePatterns` using Zstandard level 3; files over the default 1 MiB limit are skipped, `--max-size` changes that limit, `--include` adds patterns, `--pattern` replaces them, and `--output` changes the destination.
- `default.pgo` contains the representative small-file streaming profile used by `mise run build`.

## Verification
- Use the folder's mise.toml or README commands when present.
