package main

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveFilterAcrossFormats(t *testing.T) {
	for _, tool := range []string{"git", "tar", "zstd"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is required for archive integration", tool)
		}
	}
	root := t.TempDir()
	writeAutoFiles(t, root, map[string]string{
		".gitignore":              "ignored.*\n!ignored.keep\ntracked.go\n",
		"nested/.gitignore":       "*.tmp\n!keep.tmp\n",
		"nested/drop.tmp":         "ignored\n",
		"nested/keep.tmp":         "nested rule permits this\n",
		"ignored.txt":             "ignored\n",
		"ignored.keep":            "permitted\n",
		"tracked.go":              "package tracked\n",
		"unknown.newext":          "useful source\n",
		"no_extension":            "useful text\n",
		"main.go":                 "package main\n",
		"diagram.svg":             "<svg></svg>\n",
		"renamed.data":            "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR",
		"renamed-svg.data":        "<?xml version=\"1.0\"?><svg></svg>",
		"binary.mystery":          "foo\x00bar",
		"huge.secret":             strings.Repeat("x", 2000),
		"huge.txt":                strings.Repeat("x", 2000),
		".env":                    "TOKEN=hidden\n",
		"public.env.production":   "TOKEN=hidden\n",
		"public.env.example":      "TOKEN=example\n",
		"id_ed25519":              "private\n",
		".aws/config":             "access credentials\n",
		"renamed.txt":             "-----BEGIN PRIVATE KEY-----\nsecret\n",
		"node_modules/nested.txt": "dependency\n",
	})
	runTestAIGit(t, root, "init", "-q")
	runTestAIGit(t, root, "add", "-f", "tracked.go")

	for _, mode := range []struct {
		name   string
		flags  []string
		output string
	}{
		{"zip", []string{"pack"}, "out.zip"},
		{"tar-files", []string{"archive", "--files"}, "out.tar.zst"},
		{"tar-ai-files", []string{"archive", "--ai", "--files"}, "out.tar.zst"},
		{"tar-formatted", []string{"archive"}, "out.tar.zst"},
		{"tar-ai-formatted", []string{"archive", "--ai"}, "out.tar.zst"},
	} {
		t.Run(mode.name, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), mode.output)
			args := append(append([]string{}, mode.flags...), "--explain", "--max-size=1KiB", "--output="+output, root)
			var stderr bytes.Buffer
			if err := run(args, io.Discard, &stderr); err != nil {
				t.Fatalf("run: %v\n%s", err, &stderr)
			}
			var entries map[string][]byte
			if mode.name == "zip" {
				entries = readTestZIP(t, output)
			} else {
				entries = readTestTarZstd(t, output)
			}
			formatted := entries["export.txt"]
			for _, name := range []string{"unknown.newext", "no_extension", "nested/keep.tmp", "ignored.keep", "tracked.go", "public.env.example"} {
				if _, ok := entries[name]; !ok && !bytes.Contains(formatted, []byte("<<<FILE: \""+name+"\">>>")) {
					t.Errorf("missing accepted file %s", name)
				}
			}
			for _, name := range []string{"nested/drop.tmp", "ignored.txt", "diagram.svg", "renamed.data", "renamed-svg.data", "binary.mystery", "huge.secret", "huge.txt", ".env", "public.env.production", "id_ed25519", ".aws/config", "renamed.txt"} {
				if _, ok := entries[name]; ok || bytes.Contains(formatted, []byte("<<<FILE: \""+name+"\">>>")) {
					t.Errorf("included rejected file %s", name)
				}
			}
			for _, explanation := range []string{"Skipping (gitignore): ignored.txt", "Skipping (sensitive path): huge.secret", "Skipping (too large): huge.txt", "Skipping (media or archive content): renamed.data", "Skipping (private key content): renamed.txt", "Skipping (ignored directory): node_modules"} {
				if !strings.Contains(stderr.String(), explanation) {
					t.Errorf("missing explanation %q: %s", explanation, &stderr)
				}
			}
		})
	}
}

func readTestTarZstd(t *testing.T, path string) map[string][]byte {
	t.Helper()
	command := exec.Command("zstd", "-q", "-d", "-c", path)
	data, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	entries := make(map[string][]byte)
	reader := tar.NewReader(bytes.NewReader(data))
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return entries
		}
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		entries[header.Name] = content
	}
}

func TestArchiveFilterNestedRepositoriesAndMissingGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	root := t.TempDir()
	writeAutoFiles(t, root, map[string]string{
		".gitignore":         "nested/\n",
		"main.go":            "package main\n",
		"nested/.gitignore":  "*.ignore\n",
		"nested/inside.go":   "package nested\n",
		"nested/drop.ignore": "ignored\n",
	})
	runTestAIGit(t, root, "init", "-q")
	runTestAIGit(t, filepath.Join(root, "nested"), "init", "-q")
	var stderr bytes.Buffer
	output := filepath.Join(t.TempDir(), "out.zip")
	if err := run([]string{"pack", "--explain", "--output=" + output, root}, io.Discard, &stderr); err != nil {
		t.Fatal(err)
	}
	entries := readTestZIP(t, output)
	if _, ok := entries["nested/inside.go"]; !ok {
		t.Fatal("nested repository did not use its own ignore rules")
	}
	if _, ok := entries["nested/drop.ignore"]; ok {
		t.Fatal("nested repository ignore rule was skipped")
	}
	t.Setenv("PATH", t.TempDir())
	if err := run([]string{"pack", "--output=" + output, root}, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "Git is required") {
		t.Fatalf("missing Git should fail safely: %v", err)
	}
}

func TestArchiveFilterGitignoreOutsideRepository(t *testing.T) {
	root := t.TempDir()
	writeAutoFiles(t, root, map[string]string{
		".gitignore": "*.tmp\n!keep.tmp\nblocked/\n!blocked/keep.txt\n/restrict.txt\n**/glob.txt\n\\#literal\n\\!literal\n",
		"main.go":    "package main\n", "drop.tmp": "ignored\n", "keep.tmp": "kept\n",
		"blocked/keep.txt": "blocked parent\n", "restrict.txt": "root only\n",
		"nested/restrict.txt": "not rooted\n", "nested/glob.txt": "glob ignored\n",
		"#literal": "ignored\n", "!literal": "ignored\n",
		"nested/.gitignore": "!keep.tmp\nnested-only\n",
		"nested/keep.tmp":   "kept nested\n", "nested/nested-only": "ignored nested\n",
		"nested/other.tmp": "ignored by parent\n", "binary.data": "data\x00binary",
	})
	// The fallback must work without the Git executable.
	t.Setenv("PATH", t.TempDir())
	output := filepath.Join(t.TempDir(), "archive.zip")
	var stderr bytes.Buffer
	if err := run([]string{"pack", "--explain", "--output=" + output, root}, io.Discard, &stderr); err != nil {
		t.Fatalf("pack: %v\n%s", err, &stderr)
	}
	entries := readTestZIP(t, output)
	for _, name := range []string{"main.go", "keep.tmp", "nested/keep.tmp", "nested/restrict.txt"} {
		if _, ok := entries[name]; !ok {
			t.Errorf("missing non-ignored file %s", name)
		}
	}
	for _, name := range []string{"drop.tmp", "blocked/keep.txt", "restrict.txt", "nested/glob.txt", "#literal", "!literal", "nested/nested-only", "nested/other.tmp", "binary.data"} {
		if _, ok := entries[name]; ok {
			t.Errorf("unexpected ignored or binary file %s", name)
		}
	}
	if !strings.Contains(stderr.String(), "Skipping (gitignore): nested/nested-only") {
		t.Fatalf("missing ignore explanation: %s", &stderr)
	}
}

func TestStandaloneGitignoreMatchesGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for comparison")
	}
	root := t.TempDir()
	writeAutoFiles(t, root, map[string]string{
		".gitignore": "*.tmp\n!keep.tmp\nblocked/\n!blocked/keep.txt\nopen/*\n!open/keep\n/root.txt\n**/glob.txt\n\\#literal\n\\!literal\nspace\\ \nname?.txt\npart[ab].txt\n",
		"main.go":    "main\n", "drop.tmp": "ignored\n", "keep.tmp": "kept\n",
		"blocked/keep.txt": "blocked\n", "open/keep": "kept\n", "open/drop": "ignored\n",
		"root.txt": "ignored\n", "nested/root.txt": "kept\n", "nested/glob.txt": "ignored\n",
		"#literal": "ignored\n", "!literal": "ignored\n", "space ": "ignored\n",
		"name1.txt": "ignored\n", "parta.txt": "ignored\n", "partc.txt": "kept\n",
		"nested/.gitignore": "*.txt\n!root.txt\n",
	})
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := standaloneGitIgnored(root, paths)
	if err != nil {
		t.Fatal(err)
	}
	runTestAIGit(t, root, "init", "-q")
	want, err := archiveGitIgnored(root, paths)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if got[path] != want[path] {
			relative, _ := filepath.Rel(root, path)
			t.Errorf("%s: fallback ignored=%t, Git ignored=%t", relative, got[path], want[path])
		}
	}
}

func TestArchiveOutputTildeExpansion(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.Mkdir(filepath.Join(home, "Downloads"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeAutoFiles(t, root, map[string]string{"main.go": "package main\n"})
	if err := run([]string{"pack", "--output=~/Downloads/archive.zip", root}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, ok := readTestZIP(t, filepath.Join(home, "Downloads", "archive.zip"))["main.go"]; !ok {
		t.Fatal("archive missing source")
	}
}

func TestStandaloneGitignoreWithNestedRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	root := t.TempDir()
	writeAutoFiles(t, root, map[string]string{
		".gitignore": "*.tmp\n",
		"drop.tmp":   "ignored\n", "main.go": "main\n",
		"repo/.gitignore":  "*.nested\n",
		"repo/keep.tmp":    "nested repository rule permits this\n",
		"repo/drop.nested": "ignored\n",
	})
	runTestAIGit(t, filepath.Join(root, "repo"), "init", "-q")
	output := filepath.Join(t.TempDir(), "archive.zip")
	if err := run([]string{"pack", "--output=" + output, root}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	entries := readTestZIP(t, output)
	if _, ok := entries["repo/keep.tmp"]; !ok {
		t.Fatal("nested repository must use its own Git rules")
	}
	for _, name := range []string{"drop.tmp", "repo/drop.nested"} {
		if _, ok := entries[name]; ok {
			t.Errorf("ignored path included: %s", name)
		}
	}
}

func TestArchivePatternsCannotBypassSafety(t *testing.T) {
	root := t.TempDir()
	writeAutoFiles(t, root, map[string]string{
		"main.go":     "package main\n",
		"private.key": "private key\n",
		"logo.svg":    "<svg></svg>",
		"other.txt":   "not selected\n",
	})
	output := filepath.Join(t.TempDir(), "out.zip")
	var stderr bytes.Buffer
	args := []string{"pack", "--pattern=**.go,**.key,**.svg", "--explain", "--output=" + output, root}
	if err := run(args, io.Discard, &stderr); err != nil {
		t.Fatal(err)
	}
	entries := readTestZIP(t, output)
	for _, name := range []string{"private.key", "logo.svg", "other.txt"} {
		if _, ok := entries[name]; ok {
			t.Errorf("unexpected entry %s", name)
		}
	}
	for _, reason := range []string{"sensitive path", "media or binary extension", "pattern mismatch"} {
		if !strings.Contains(stderr.String(), reason) {
			t.Errorf("missing reason %q: %s", reason, &stderr)
		}
	}
}

func TestArchiveGitignorePrecedesPatterns(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	root := t.TempDir()
	writeAutoFiles(t, root, map[string]string{
		".gitignore":  "ignored.txt\n",
		"main.go":     "package main\n",
		"ignored.txt": "ignored\n",
	})
	runTestAIGit(t, root, "init", "-q")
	output := filepath.Join(t.TempDir(), "out.zip")
	var stderr bytes.Buffer
	if err := run([]string{"pack", "--pattern=**.go", "--explain", "--output=" + output, root}, io.Discard, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "Skipping (gitignore): ignored.txt") {
		t.Fatalf("Git must reject before pattern: %s", &stderr)
	}
}

func TestExplainOption(t *testing.T) {
	if _, err := parseArgs([]string{"--explain"}); err == nil {
		t.Fatal("--explain must require an archive command")
	}
	for _, shell := range supportedAutocompleteShells {
		script, err := autocompleteScript(shell)
		if err != nil || !strings.Contains(script, "explain") {
			t.Errorf("missing explain completion for %s: %v", shell, err)
		}
	}
}
