# Plan

## Goal

Make AI-enabled Promptcat archives report Git context for every repository
contained under the selected archive folder, while keeping the archive itself
combined and preserving the existing root-level metadata fields.

## Confirmed

- `promptcat archive` walks the selected folder and combines matched files with
  paths relative to that folder.
- `archive --ai` currently captures Git metadata only from the selected root.
- Nested repositories are otherwise included as ordinary folders, except for
  their excluded `.git` metadata directories.
- The AI manifest schema currently has singular `git` and `changes` fields.

## Assumptions

- Keep the combined archive layout unchanged; represent repositories in
  `.promptcat/project.json` rather than creating separate archives.
- Add a `repositories` array with each repository's archive-root-relative path,
  Git metadata, and archived working-tree changes.
- Retain `git` and `changes` as backward-compatible metadata for the archive
  root (or its enclosing repository when the selected root is a subfolder).
- Detect nested repositories from Git's reported top-level directory, not just
  the presence of a `.git` marker, so worktrees and submodules are supported.
- Ignore directories already excluded from the archive while discovering
  nested repositories, and do not follow symlinks.

## Todo

- [x] Add per-repository manifest metadata and nested repository discovery.
- [x] Filter Git working-tree changes to files actually included in the archive
      and store their paths relative to the archive root.
- [x] Add tests for nested sibling repositories, linked worktrees, and unrelated
      directories, including per-repository change records.
- [x] Update project context and user documentation for the `--ai` manifest.
- [x] Run formatting, full tests, build, vet, and diff checks; fix any failures.
- [ ] Commit and push the scoped change, then create the next patch release tag
      and verify the GitHub release workflow result.

## Implementation Order

1. Extend the AI manifest types while preserving the existing fields.
2. Discover repository roots under the selected folder and collect each
   repository's Git state against its archived file subset.
3. Add regression tests and update the archive documentation.
4. Run all project checks and inspect the final diff.
5. Commit, push, tag a release, and inspect CI/release status.

## Risks

- Running Git from the archive root for a nested file can incorrectly return
  the parent repository; discovery must identify exact repository roots.
- Paths from Git status are repository-relative and must be translated to
  archive-root-relative paths without collisions or traversal.
- Repositories nested under default-excluded directories should not be
  discovered, because their source files are not part of the archive.
- Release creation is an external GitHub side effect and must happen only after
  the verified commit has been pushed.

## Verification

- Tests assert repository roots and manifest paths/metadata for a parent repo
  with nested repositories and for unrelated nested folders.
- Tests assert the old `git` and `changes` fields retain their existing root
  semantics.
- `go test ./...`, `go vet ./...`, project build, and `git diff --check` pass.
- GitHub Actions completes the release for the pushed tag.
