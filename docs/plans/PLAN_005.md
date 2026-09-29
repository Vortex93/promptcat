# Plan

## Goal

Allow archive users to exclude selected repository folders from source files and AI archive data.

## Decisions

- Add repeatable `--exclude-repo=PATH` and `--exclude-repo PATH` forms.
- Interpret paths relative to the archive folder and prune the full named folder subtree.
- Excluded files must not contribute to AI metadata or patches.
- Keep archive defaults and repository discovery unchanged.

## Verification

- Test parsing, invalid paths, and traversal pruning with multiple sibling repositories.
- Run the Promptcat Go tests, build, vet, and `git diff --check`.
- Inspect the final diff and preserve unrelated working-tree files.
