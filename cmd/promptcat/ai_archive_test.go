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
	if manifest.Mode != "lazy" || manifest.SchemaVersion != 4 || manifest.FileCount != len(files) {
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
