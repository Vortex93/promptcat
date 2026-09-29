package main

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestWriteAIArchivePackUsesLazyScanner(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"backend/card.go": `package card

type CardService struct{}
func (s *CardService) GetCards() []string { return nil }
`,
		"frontend/card.ts": `export class CardStore {
    getCards() { return []; }
}
`,
		"frontend/controller.ts": `import { CardStore } from "./card";
export function loadCards(store: CardStore) { return store.getCards(); }
`,
	}

	paths := make([]string, 0, len(files))
	for path, content := range files {
		fullPath := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}

	destination := t.TempDir()
	generated, err := writeAIArchivePack(root, paths, destination)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	want := []string{".promptcat/AI_INSTRUCTIONS.md", ".promptcat/tools.py", ".promptcat/project.json"}
	if strings.Join(generated, "\n") != strings.Join(want, "\n") {
		t.Fatalf("generated = %#v, want %#v", generated, want)
	}
	for _, removed := range []string{"files.jsonl", "symbols.jsonl", "imports.jsonl", "references.jsonl", "calls.jsonl", "tests.jsonl", "repo-map.txt"} {
		if _, err := os.Stat(filepath.Join(destination, aiPackDirectory, removed)); !os.IsNotExist(err) {
			t.Fatalf("lazy AI pack unexpectedly generated %s", removed)
		}
	}

	data, err := os.ReadFile(filepath.Join(destination, aiPackDirectory, "project.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest aiProjectManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Mode != "lazy" || manifest.SchemaVersion != 6 || manifest.FileCount != len(files) {
		t.Fatalf("unexpected lazy manifest: %#v", manifest)
	}
	if manifest.Languages["go"] == 0 || manifest.Languages["typescript"] == 0 {
		t.Fatalf("manifest missing languages: %#v", manifest.Languages)
	}

	tools, err := os.ReadFile(filepath.Join(destination, aiPackDirectory, "tools.py"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(tools), "def symbols(") || strings.Contains(string(tools), "symbols.jsonl") {
		t.Fatal("tools.py is not using lazy symbol scanning")
	}

	python, err := exec.LookPath("python3")
	if err != nil {
		return
	}
	var export strings.Builder
	for _, path := range paths {
		export.WriteString("<<<FILE: ")
		export.WriteString(strconv.Quote(filepath.ToSlash(path)))
		export.WriteString(">>>\n")
		export.WriteString(strings.TrimRight(files[path], "\n"))
		export.WriteString("\n<<<END FILE>>>\n\n")
	}
	if err := os.WriteFile(filepath.Join(destination, "export.txt"), []byte(export.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(python, filepath.Join(destination, aiPackDirectory, "tools.py"), "callers", "getCards", "--json")
	command.Dir = destination
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("lazy callers scan failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), `"caller": "loadCards"`) {
		t.Fatalf("lazy callers scan missed loadCards: %s", output)
	}
}

func TestApplyArchiveDefaultsForAI(t *testing.T) {
	opts := options{archiveAI: true}
	applyArchiveDefaults(&opts)
	if !opts.ignoredDirs[".agentpack"] {
		t.Fatal("AI archives should ignore .agentpack by default")
	}
	for _, want := range []string{"**.md", "**/go.mod", "**/package.json", "**/Cargo.toml", "**/pyproject.toml", "**/Dockerfile", "**/Makefile"} {
		found := false
		for _, pattern := range opts.archivePatterns {
			if pattern == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("AI archive patterns missing %q", want)
		}
	}
}

func TestCollectAIRepositoriesAcrossSiblingRepositories(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for repository metadata tests")
	}
	root := t.TempDir()
	files := []string{
		"alpha/alpha.go",
		"beta/beta.ts",
		"notes/readme.md",
	}
	for _, relative := range files {
		repositoryPath := filepath.Join(root, filepath.Dir(relative))
		if err := os.MkdirAll(repositoryPath, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, relative), []byte("initial\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	initTestAIRepository(t, filepath.Join(root, "alpha"), "alpha.go")
	initTestAIRepository(t, filepath.Join(root, "beta"), "beta.ts")
	if err := os.WriteFile(filepath.Join(root, "alpha", "alpha.go"), []byte("modified\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "beta", "beta.ts"), []byte("modified\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	repositories, err := collectAIRepositories(root, files)
	if err != nil {
		t.Fatal(err)
	}
	if len(repositories) != 2 {
		t.Fatalf("found %d repositories, want 2: %#v", len(repositories), repositories)
	}
	wantPaths := []string{"alpha", "beta"}
	for index, repository := range repositories {
		if repository.Path != wantPaths[index] {
			t.Errorf("repository[%d].Path = %q, want %q", index, repository.Path, wantPaths[index])
		}
		if !repository.Git.Available || !repository.Git.Dirty {
			t.Errorf("repository[%q] git metadata = %#v, want available and dirty", repository.Path, repository.Git)
		}
		if len(repository.Changes) != 1 || repository.Changes[0].Path != files[index] || !repository.Changes[0].Archived {
			t.Errorf("repository[%q] changes = %#v", repository.Path, repository.Changes)
		}
	}

	destination := t.TempDir()
	if _, err := writeAIArchivePack(root, files, destination); err != nil {
		t.Fatal(err)
	}
	manifestData, err := os.ReadFile(filepath.Join(destination, aiPackDirectory, "project.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest aiProjectManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Repositories) != 2 || manifest.Repositories[0].Path != "alpha" || manifest.Repositories[1].Path != "beta" {
		t.Fatalf("manifest.repositories = %#v, want alpha and beta", manifest.Repositories)
	}
}

func TestDiscoverAIRepositoryRootsIncludesSelectedRootAndWorktrees(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for repository metadata tests")
	}
	root := t.TempDir()
	initTestAIRepository(t, root, "main.go")
	worktreePath := filepath.Join(root, "linked")
	if output, err := exec.Command("git", "-C", root, "worktree", "add", "-b", "linked-branch", worktreePath).CombinedOutput(); err != nil {
		t.Fatalf("create linked worktree: %v\n%s", err, output)
	}

	repositories, err := discoverAIRepositoryRoots(root)
	if err != nil {
		t.Fatal(err)
	}
	wantPaths := []string{".", "linked"}
	if len(repositories) != len(wantPaths) {
		t.Fatalf("repository roots = %#v, want %v", repositories, wantPaths)
	}
	for index, want := range wantPaths {
		if repositories[index].path != want {
			t.Errorf("repository[%d].path = %q, want %q", index, repositories[index].path, want)
		}
	}
}

func TestWriteAIRepositoryPatchesIncludesCommitsAndFilteredWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for repository patch tests")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "included.go"), []byte("package example\n\nconst Value = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "excluded.json"), []byte("{\"secret\":\"baseline\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	initTestAIRepository(t, root, "included.go")
	runTestAIGit(t, root, "add", "excluded.json")
	runTestAIGit(t, root, "-c", "user.name=Promptcat Test", "-c", "user.email=promptcat-test@example.invalid", "commit", "-q", "-m", "add excluded data")
	base := runTestAIGit(t, root, "rev-parse", "HEAD")
	runTestAIGit(t, root, "update-ref", "refs/remotes/origin/base", base)
	runTestAIGit(t, root, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/base")
	if err := os.WriteFile(filepath.Join(root, "included.go"), []byte("package example\n\nconst Value = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runTestAIGit(t, root, "add", "included.go")
	runTestAIGit(t, root, "-c", "user.name=Promptcat Test", "-c", "user.email=promptcat-test@example.invalid", "commit", "-q", "-m", "first worktree commit")
	firstCommit := runTestAIGit(t, root, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(root, "included.go"), []byte("package example\n\nconst Value = 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runTestAIGit(t, root, "add", "included.go")
	runTestAIGit(t, root, "-c", "user.name=Promptcat Test", "-c", "user.email=promptcat-test@example.invalid", "commit", "-q", "-m", "second worktree commit")
	secondCommit := runTestAIGit(t, root, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(root, "included.go"), []byte("package example\n\nconst Value = 4 // staged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runTestAIGit(t, root, "add", "included.go")
	if err := os.WriteFile(filepath.Join(root, "included.go"), []byte("package example\n\nconst Value = 4 // unstaged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "excluded.json"), []byte("{\"secret\":\"must-not-be-in-patch\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	repositories, err := collectAIRepositories(root, []string{"included.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(repositories) != 1 || repositories[0].Path != "." {
		t.Fatalf("repositories = %#v, want only the selected root repository", repositories)
	}
	packDir := t.TempDir()
	patches, paths, err := writeAIRepositoryPatches(root, []string{"included.go"}, repositories, packDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(patches) != 3 || len(paths) != 3 {
		t.Fatalf("patch count = %d/%d, want two commits and one worktree patch: %#v", len(patches), len(paths), patches)
	}
	wantPaths := map[string]bool{
		".promptcat/patches/root/" + firstCommit + ".patch":  false,
		".promptcat/patches/root/" + secondCommit + ".patch": false,
		".promptcat/patches/root/worktree.patch":             false,
	}
	for _, patch := range patches {
		if _, ok := wantPaths[patch.Path]; !ok {
			t.Errorf("unexpected patch metadata path %q", patch.Path)
		}
		wantPaths[patch.Path] = true
		data, err := os.ReadFile(filepath.Join(packDir, filepath.FromSlash(strings.TrimPrefix(patch.Path, aiPackDirectory+"/"))))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "included.go") {
			t.Errorf("patch %q does not contain included.go", patch.Path)
		}
		if strings.Contains(string(data), "excluded.json") || strings.Contains(string(data), "must-not-be-in-patch") {
			t.Errorf("patch %q leaked an excluded file", patch.Path)
		}
	}
	for path, found := range wantPaths {
		if !found {
			t.Errorf("missing expected patch %q", path)
		}
	}

	destination := t.TempDir()
	packFiles, err := writeAIArchivePack(root, []string{"included.go"}, destination)
	if err != nil {
		t.Fatal(err)
	}
	for path := range wantPaths {
		found := false
		for _, packFile := range packFiles {
			if packFile == path {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("AI pack file list does not include patch %q", path)
		}
	}
	manifestData, err := os.ReadFile(filepath.Join(destination, aiPackDirectory, "project.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest aiProjectManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Patches) != 3 {
		t.Errorf("manifest patch records = %#v, want two commit patches and one worktree patch", manifest.Patches)
	}
	if _, err := exec.LookPath("zstd"); err == nil {
		opts := options{
			archiveAI:       true,
			archivePatterns: []string{"**.go"},
			archiveOutput:   "result.tar.zst",
			inputs:          []string{root},
		}
		applyArchiveDefaults(&opts)
		if err := runArchive(opts, io.Discard); err != nil {
			t.Fatalf("create AI archive with patches: %v", err)
		}
		archivePath := filepath.Join(root, "result.tar.zst")
		compressed, err := exec.Command("zstd", "-d", "-q", "-c", archivePath).Output()
		if err != nil {
			t.Fatalf("decompress generated archive: %v", err)
		}
		reader := tar.NewReader(bytes.NewReader(compressed))
		archiveEntries := make(map[string]bool)
		for {
			header, err := reader.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("read generated archive: %v", err)
			}
			archiveEntries[header.Name] = true
		}
		for path := range wantPaths {
			if !archiveEntries[path] {
				t.Errorf("generated tar.zst is missing patch %q", path)
			}
		}
	} else {
		t.Log("zstd is unavailable; skipped compressed archive smoke test")
	}
}

func TestResolveAIArchiveBaseFallsBackToConfiguredUpstream(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for repository patch tests")
	}
	root := t.TempDir()
	initTestAIRepository(t, root, "main.go")
	base := runTestAIGit(t, root, "rev-parse", "HEAD")
	runTestAIGit(t, root, "branch", "topic", base)
	runTestAIGit(t, root, "branch", "--set-upstream-to=topic")
	if got := resolveAIArchiveBase(root); got != base {
		t.Fatalf("resolveAIArchiveBase() = %q, want upstream commit %q", got, base)
	}
}

func TestArchiveFilesByRepositoryAssignsFilesToInnermostRepository(t *testing.T) {
	root := t.TempDir()
	repositories := []aiRepositoryRoot{
		{path: ".", root: root},
		{path: "nested", root: filepath.Join(root, "nested")},
	}
	files := []string{"root.go", "nested/module.go"}
	got := archiveFilesByRepository(root, files, repositories)
	if len(got[root]) != 1 || got[root]["root.go"] != "root.go" {
		t.Errorf("outer repository files = %#v, want only root.go", got[root])
	}
	if len(got[repositories[1].root]) != 1 || got[repositories[1].root]["module.go"] != "nested/module.go" {
		t.Errorf("nested repository files = %#v, want module.go mapped to nested/module.go", got[repositories[1].root])
	}
}

func TestWriteAIRepositoryPatchesPlacesNestedRepositoryUnderRepoPath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for repository patch tests")
	}
	archiveRoot := t.TempDir()
	repositoryRoot := filepath.Join(archiveRoot, "nested")
	initTestAIRepository(t, repositoryRoot, "module.go")
	base := runTestAIGit(t, repositoryRoot, "rev-parse", "HEAD")
	runTestAIGit(t, repositoryRoot, "update-ref", "refs/remotes/origin/base", base)
	runTestAIGit(t, repositoryRoot, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/base")
	if err := os.WriteFile(filepath.Join(repositoryRoot, "module.go"), []byte("package module\n\nconst Value = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runTestAIGit(t, repositoryRoot, "add", "module.go")
	runTestAIGit(t, repositoryRoot, "-c", "user.name=Promptcat Test", "-c", "user.email=promptcat-test@example.invalid", "commit", "-q", "-m", "nested feature")
	commit := runTestAIGit(t, repositoryRoot, "rev-parse", "HEAD")

	repositories, err := collectAIRepositories(archiveRoot, []string{"nested/module.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(repositories) != 1 || repositories[0].Path != "nested" {
		t.Fatalf("repositories = %#v, want nested repository", repositories)
	}
	packDir := t.TempDir()
	patches, _, err := writeAIRepositoryPatches(archiveRoot, []string{"nested/module.go"}, repositories, packDir)
	if err != nil {
		t.Fatal(err)
	}
	wantPath := ".promptcat/patches/repo/nested/" + commit + ".patch"
	if len(patches) != 1 || patches[0].Path != wantPath {
		t.Fatalf("patches = %#v, want patch at %q", patches, wantPath)
	}
}

func initTestAIRepository(t *testing.T, root, file string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(root, file)
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		if err := os.WriteFile(filePath, []byte("initial\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if output, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("initialize git repository: %v\n%s", err, output)
	}
	if output, err := exec.Command("git", "-C", root, "add", file).CombinedOutput(); err != nil {
		t.Fatalf("add test file: %v\n%s", err, output)
	}
	command := exec.Command("git", "-C", root, "-c", "user.name=Promptcat Test", "-c", "user.email=promptcat-test@example.invalid", "commit", "-q", "-m", "initial")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("commit test repository: %v\n%s", err, output)
	}
}

func runTestAIGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}
