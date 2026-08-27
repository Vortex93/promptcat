# CONTEXT.md

## Purpose
- Fast boot summary for `apps/promptcat/`.

## Current Shape
- Top-level app root with project files and local instructions.
- Positional `!glob` arguments exclude expanded file paths; `--exclude` remains extension-only.
- Project workflows use `mise.toml`; multi-step build and release tasks run through `scripts/*.mjs` with ZX.
- Symlinks are skipped; auto mode ignores Python environments and cache directories.
- Directory walks avoid per-directory sorting, final glob results remain deterministic, and file output uses safe serial streaming.

## Verification
- Use the folder's mise.toml or README commands when present.
