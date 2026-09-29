# Plan

## Goal

Include Git patches in `promptcat archive --ai` for commits on detected worktree
branches that are ahead of the repository's detected base, plus tracked staged
and unstaged changes.

## Confirmed

- AI archives already identify each repository and record branch, commit,
  upstream, default branch, and included changed-file metadata.
- AI pack files are written under `.promptcat/` in the generated archive.
- The archive's matched-file list provides a safe allowlist for patch paths.

## Assumptions

- Commit patches compare against `origin/HEAD`'s branch when available, then
  fall back to the configured upstream; if neither resolves, omit commit patches.
- Write one `.promptcat/patches/<repo-path>/<commit-hash>.patch` per commit
  ahead of that base, using a dedicated `root/` folder for the selected root
  repository and `repo/<repo-path>/` for nested repositories.
- Write `worktree.patch` beside each repository's commit patches for staged
  and unstaged changes to tracked files.
- Restrict every patch to files already included in the archive. Untracked
  files remain present as source entries but are not duplicated into patches.
- Keep all patches in `--ai` archives only; regular archive behavior stays
  unchanged.

## Todo

- [x] Generate per-commit and tracked-worktree patches for each detected repo.
- [x] List patch file paths, repository, kind, and commit hash in project JSON.
- [x] Test base selection, path filtering, dirty worktree output, and multi-repo
      patch naming; update project documentation.
- [x] Run formatting, full tests, build, vet, diff checks, and archive smoke
      verification; fix any failures.
- [ ] Commit and push the change, publish the next patch release, and verify
      GitHub Actions and release assets.

## Implementation Order

1. Add patch metadata records and bounded Git command helpers.
2. Generate filtered commit patches and a filtered worktree patch per repo.
3. Add focused Git-fixture tests and document exact patch behavior.
4. Run project checks and inspect generated archive contents.
5. Commit, push, release, and verify the published assets.

## Risks

- Patch generation must honor the archive's selected-file and max-size filters
  to avoid disclosing excluded repository files.
- A branch base cannot always be inferred. Prefer the remote default branch and
  use upstream only if that is unavailable; record patch metadata so consumers
  can understand exactly what was included.
- Binary changes can make patches large, but only for files already selected
  into the archive.
- Git diff returns exit status 1 when differences exist; this is not a command
  failure.

## Verification

- Tests inspect patch filenames and contents from synthetic parent/nested repos.
- Tests prove excluded files do not appear in generated patches.
- `mise run test`, `mise run build`, `go vet ./...`, and `git diff --check` pass.
- A generated `.tar.zst` passes `zstd -t` and its entries include expected
  `.promptcat/patches/` files.
- GitHub Actions succeeds and the release contains platform archives/checksums.
