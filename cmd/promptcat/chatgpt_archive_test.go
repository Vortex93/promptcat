package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseArgsChatGPT(t *testing.T) {
	for _, args := range [][]string{
		{"archive", "--chatgpt"},
		{"--chatgpt", "archive", "--ai", "--files", "--clipboard"},
		{"archive", "--chatgpt", "--output=review.ZIP", "--include=**.txt"},
	} {
		opts, err := parseArgs(args)
		if err != nil {
			t.Fatalf("parseArgs(%q): %v", args, err)
		}
		if !opts.archiveChatGPT || !opts.archiveAI || !opts.archiveFiles || !strings.EqualFold(filepath.Ext(opts.archiveOutput), ".zip") {
			t.Fatalf("unexpected ChatGPT preset: %#v", opts)
		}
		if opts.maxSize != defaultArchiveMaxSize || !opts.ignoredDirs[".promptcat"] {
			t.Fatalf("archive safety defaults were lost: %#v", opts)
		}
	}
	for _, args := range [][]string{
		{"--chatgpt"}, {"auto", "--chatgpt"}, {"update", "--chatgpt"},
		{"autocomplete", "bash", "--chatgpt"},
		{"archive", "--chatgpt", "auto"},
		{"archive", "--files", "--clipboard"},
	} {
		if _, err := parseArgs(args); err == nil {
			t.Errorf("parseArgs(%q) unexpectedly succeeded", args)
		}
	}
	legacy, err := parseArgs([]string{"archive", "--ai"})
	if err != nil || legacy.archiveFiles || legacy.archiveOutput != "archive.tar.zst" {
		t.Fatalf("legacy archive defaults changed: %#v, %v", legacy, err)
	}
	for _, shell := range supportedAutocompleteShells {
		script, err := autocompleteScript(shell)
		if err != nil || !strings.Contains(script, "chatgpt") {
			t.Errorf("missing ChatGPT completion for %s: %v", shell, err)
		}
	}
}

func TestChatGPTArchivePreservesBytesAndInventory(t *testing.T) {
	root := t.TempDir()
	included := map[string]string{
		"README.md":        "# Demo\n",
		"go.mod":           "module example\n\ngo 1.23\n",
		"src/main.go":      "package main\r\n\r\nfunc main() {}", // CRLF, no final newline.
		"src/empty.ts":     "",
		"src/hello λ.ts":   "export const text = 'café';\n",
		"src/main_test.go": "package main\n",
		"scripts/check.sh": "#!/bin/sh\nprintf ok\n",
		"export.txt":       "<<<FILE: \"not-a-real-file.go\">>>\nliteral marker\n<<<END FILE>>>\n",
	}
	writeAutoFiles(t, root, included)
	writeAutoFiles(t, root, map[string]string{
		"src/binary.ts":           "before\x00after",
		"src/latin1.py":           "# caf\xe9\n",
		"src/large.go":            strings.Repeat("x", 2048),
		"node_modules/private.ts": "do not include\n",
		"excluded/secret.go":      "do not include\n",
		".promptcat/project.json": "do not overwrite metadata\n",
	})
	output := filepath.Join(root, "review.zip")
	var stderr bytes.Buffer
	args := []string{"archive", "--chatgpt", "--include=**.txt", "--max-size=1KiB", "--exclude-repo=excluded", "--output=review.zip", root}
	if err := run(args, io.Discard, &stderr); err != nil {
		t.Fatal(err)
	}
	entries := readTestZIP(t, output)
	var index chatGPTFileIndex
	if err := json.Unmarshal(entries[chatGPTIndexPath], &index); err != nil {
		t.Fatal(err)
	}
	if index.Layout != "files" || len(index.Files) != len(included) {
		t.Fatalf("unexpected inventory: %#v", index)
	}
	for _, record := range index.Files {
		want, ok := included[record.Path]
		if !ok || !bytes.Equal(entries[record.Path], []byte(want)) {
			t.Errorf("unexpected/modified source: %s", record.Path)
		}
		hash := sha256.Sum256(entries[record.Path])
		if record.SHA256 != fmt.Sprintf("%x", hash) || record.Size != int64(len(want)) {
			t.Errorf("incorrect integrity metadata: %#v", record)
		}
		lines := strings.Count(want, "\n")
		if len(want) > 0 && !strings.HasSuffix(want, "\n") {
			lines++
		}
		if record.Lines != lines {
			t.Errorf("wrong line count for %s: %d != %d", record.Path, record.Lines, lines)
		}
	}
	var manifest aiProjectManifest
	if err := json.Unmarshal(entries[aiPackDirectory+"/project.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.FileCount != len(included) {
		t.Fatalf("project count includes omitted files: %d", manifest.FileCount)
	}
	if !reflect.DeepEqual(index.Selection.ExcludedRepos, []string{"excluded"}) || index.Selection.MaxFileBytes != 1024 {
		t.Fatalf("selection settings not recorded: %#v", index.Selection)
	}
	reasons := make(map[string]string)
	for _, skipped := range index.Skipped {
		reasons[skipped.Path] = skipped.Reason
		if _, ok := entries[skipped.Path]; ok {
			t.Errorf("skipped source was archived: %s", skipped.Path)
		}
	}
	wantReasons := map[string]string{".promptcat": "ignored directory", "excluded": "excluded repository", "node_modules": "ignored directory", "src/binary.ts": "binary content", "src/latin1.py": "non-UTF-8 text", "src/large.go": "too large"}
	if !reflect.DeepEqual(reasons, wantReasons) {
		t.Fatalf("skips = %#v, want %#v", reasons, wantReasons)
	}
	for _, excluded := range []string{"node_modules/private.ts", "excluded/secret.go", "review.zip"} {
		if _, ok := entries[excluded]; ok {
			t.Errorf("unexpected archived path: %s", excluded)
		}
	}
	if len(entries[chatGPTGuidePath]) == 0 {
		t.Fatal("missing start guide")
	}
	// Replacing an existing archive must still exclude the output itself.
	if err := run(args, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, ok := readTestZIP(t, output)["review.zip"]; ok {
		t.Fatal("archive included itself")
	}
}

func TestChatGPTArchiveNeedsNoExternalArchiver(t *testing.T) {
	root := t.TempDir()
	writeAutoFiles(t, root, map[string]string{"main.go": "package main\n"})
	t.Setenv("PATH", t.TempDir()) // No git, tar, zstd, zip, or Python.
	output := filepath.Join(t.TempDir(), "review.zip")
	if err := run([]string{"archive", "--chatgpt", "--output=" + output, root}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := string(readTestZIP(t, output)["main.go"]); got != "package main\n" {
		t.Fatalf("unexpected source: %q", got)
	}
}

func TestLoadChatGPTSourcePreservesValidatedBytes(t *testing.T) {
	root := t.TempDir()
	writeAutoFiles(t, root, map[string]string{"run.sh": "#!/bin/sh\necho original\n", "binary.ts": strings.Repeat("a", 9000) + "\x00"})
	if err := os.Chmod(filepath.Join(root, "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	source, reason, err := loadChatGPTSource(root, "run.sh", defaultArchiveMaxSize)
	if err != nil || reason != "" {
		t.Fatalf("load source: %v %s", err, reason)
	}
	if err := os.WriteFile(filepath.Join(root, "run.sh"), []byte("changed\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if string(source.data) != "#!/bin/sh\necho original\n" || source.record.Size != int64(len(source.data)) || source.mode != 0o755 {
		t.Fatalf("validated bytes changed after source update: %#v", source)
	}
	_, reason, err = loadChatGPTSource(root, "binary.ts", defaultArchiveMaxSize)
	if err != nil || reason != "binary content" {
		t.Fatalf("NUL after sniff window not rejected: %s %v", reason, err)
	}
	if _, _, err := loadChatGPTSource(root, ".promptcat/evil.go", 1024); err == nil {
		t.Fatal("reserved metadata path accepted")
	}
}

func TestWriteZIPFailurePreservesDestination(t *testing.T) {
	root := t.TempDir()
	writeAutoFiles(t, root, map[string]string{"ok.go": "package ok\n"})
	output := filepath.Join(t.TempDir(), "review.zip")
	if err := os.WriteFile(output, []byte("old archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, paths := range [][]string{
		{"ok.go", "missing.go"}, {"../outside.go"}, {"/absolute.go"},
		{"a/../ok.go"}, {"a\\b.go"}, {"C:/drive.go"}, {"ok.go", "ok.go"},
	} {
		err := writeZIP(output, func(z *zipEntries) error {
			for _, relative := range paths {
				if err := z.writeFile(root, relative); err != nil {
					return err
				}
			}
			return nil
		})
		if err == nil {
			t.Errorf("unsafe/incomplete ZIP succeeded: %q", paths)
		}
		data, err := os.ReadFile(output)
		if err != nil || string(data) != "old archive" {
			t.Fatalf("existing output damaged: %q, %v", data, err)
		}
		leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(output), ".promptcat-*.zip.tmp"))
		if len(leftovers) != 0 {
			t.Fatalf("temporary output leaked: %v", leftovers)
		}
	}
	if err := run([]string{"archive", "--chatgpt", "--max-size=1B", "--output=" + output, root}, io.Discard, io.Discard); err == nil {
		t.Fatal("all-omitted archive succeeded")
	}
	data, _ := os.ReadFile(output)
	if string(data) != "old archive" {
		t.Fatal("all-omitted export damaged existing output")
	}
}

func TestWriteZIPRejectsSymlinks(t *testing.T) {
	root := t.TempDir()
	writeAutoFiles(t, root, map[string]string{"source.go": "package example\n"})
	if err := os.Symlink(filepath.Join(root, "source.go"), filepath.Join(root, "link.go")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := writeZIP(filepath.Join(t.TempDir(), "review.zip"), func(z *zipEntries) error {
		return z.writeFile(root, "link.go")
	}); err == nil {
		t.Fatal("symlink ZIP entry accepted")
	}
	source, reason, err := loadChatGPTSource(root, "link.go", 1024)
	if err != nil || reason != "" || source == nil {
		t.Fatalf("source symlink: %v %s", err, reason)
	}
	if string(source.data) != "package example\n" || source.record.Path != "link.go" {
		t.Fatalf("linked source bytes or logical path lost: %#v", source)
	}
}

func TestChatGPTArchiveFiltersGitPatchScope(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	root := t.TempDir()
	writeAutoFiles(t, root, map[string]string{"keep.go": "package example\n", "binary.ts": "original\n"})
	initTestAIRepository(t, root, "keep.go")
	runTestAIGit(t, root, "add", "binary.ts")
	runTestAIGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "add candidate")
	writeAutoFiles(t, root, map[string]string{"keep.go": "package example\n// changed\n", "binary.ts": "secret\x00payload"})
	output := filepath.Join(t.TempDir(), "review.zip")
	if err := run([]string{"archive", "--chatgpt", "--output=" + output, root}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	entries := readTestZIP(t, output)
	patch := string(entries[aiPackDirectory+"/patches/root/worktree.patch"])
	if !strings.Contains(patch, "keep.go") || strings.Contains(patch, "binary.ts") || strings.Contains(patch, "secret") {
		t.Fatalf("unexpected patch scope: %s", patch)
	}
}

func TestChatGPTNavigator(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python 3 is required for navigator tests")
	}
	command := exec.Command(python, "ai_tools_test.py")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("navigator tests failed: %v\n%s", err, output)
	}
}

func readTestZIP(t *testing.T, path string) map[string][]byte {
	t.Helper()
	archive, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	entries := make(map[string][]byte)
	if len(archive.File) == 0 || archive.File[0].Name != "START_HERE.md" {
		t.Fatal("ZIP does not start with guide")
	}
	for _, file := range archive.File {
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		closeErr := reader.Close()
		if err != nil || closeErr != nil {
			t.Fatalf("read ZIP entry %s: %v %v", file.Name, err, closeErr)
		}
		if _, duplicate := entries[file.Name]; duplicate {
			t.Fatalf("duplicate ZIP entry: %s", file.Name)
		}
		entries[file.Name] = data
	}
	return entries
}

func TestChatGPTArchiveWorksBeforeFirstGitCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	root := t.TempDir()
	writeAutoFiles(t, root, map[string]string{"main.go": "package main\n"})
	runTestAIGit(t, root, "init", "-q")
	runTestAIGit(t, root, "add", "main.go")
	writeAutoFiles(t, root, map[string]string{"main.go": "package main\n// not committed yet\n"})
	output := filepath.Join(t.TempDir(), "review.zip")
	if err := run([]string{"archive", "--chatgpt", "--output=" + output, root}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	entries := readTestZIP(t, output)
	if got := string(entries["main.go"]); got != "package main\n// not committed yet\n" {
		t.Fatalf("wrong source snapshot: %q", got)
	}
	var manifest aiProjectManifest
	if err := json.Unmarshal(entries[aiPackDirectory+"/project.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	if !manifest.Git.Available || manifest.Git.Commit != "" || len(manifest.Repositories) != 1 {
		t.Fatalf("unexpected unborn Git metadata: %#v", manifest)
	}
	if !manifest.Git.Dirty || len(manifest.Changes) != 1 {
		t.Fatalf("lost staged/uncommitted change metadata: %#v", manifest)
	}
	if len(manifest.Patches) != 0 {
		t.Fatalf("unexpected patches for missing HEAD: %#v", manifest.Patches)
	}
}

func TestChatGPTArchiveSkipsLinks(t *testing.T) {
	root, target := t.TempDir(), t.TempDir()
	writeAutoFiles(t, root, map[string]string{"main.go": "package main\n"})
	writeAutoFiles(t, target, map[string]string{"skill.md": "linked skill\n", "node_modules/hidden.md": "ignored", "excluded/hidden.md": "excluded"})
	for link, destination := range map[string]string{"skills": target, "alias.md": filepath.Join(target, "skill.md"), "broken.md": filepath.Join(target, "missing")} {
		if err := os.Symlink(destination, filepath.Join(root, link)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	if err := os.Symlink(target, filepath.Join(target, "loop")); err != nil {
		t.Fatal(err)
	}
	opts, err := parseArgs([]string{"archive", "--chatgpt", "--explain", "--exclude-repo=skills/excluded", root})
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if err := runArchive(opts, io.Discard, &stderr); err != nil {
		t.Fatal(err)
	}
	entries := readTestZIP(t, filepath.Join(root, "archive.zip"))
	for name := range entries {
		if strings.Contains(name, "hidden.md") || strings.HasPrefix(name, "skills/") || name == "alias.md" || name == "broken.md" {
			t.Errorf("unexpected entry %s", name)
		}
	}
	if !strings.Contains(stderr.String(), "symlink") {
		t.Errorf("missing link warnings: %s", &stderr)
	}
}
