package main

import (
	"encoding/json"
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
	if manifest.Mode != "lazy" || manifest.SchemaVersion != 5 || manifest.FileCount != len(files) {
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
