package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestClipboardDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test clipboard backend uses a POSIX shell")
	}
	root := t.TempDir()
	writeAutoFiles(t, root, map[string]string{
		"src/main.go":           "package main\n",
		"src/app.ts":            "const app = true;\n",
		"src/binary.go":         "binary\x00data",
		"src/large.go":          strings.Repeat("x", defaultArchiveMaxSize+1),
		"node_modules/noise.go": "ignored",
		"generated/noise.go":    "ignored",
		"notes.txt":             "explicit file\n",
		".env":                  "TOKEN=hidden\n",
		"image.svg":             "<svg></svg>\n",
	})
	t.Chdir(root)
	backend := t.TempDir()
	// Capture stdin without touching the user's real clipboard.
	for _, name := range []string{"wl-copy", "pbcopy"} {
		if err := os.WriteFile(filepath.Join(backend, name), []byte("#!/bin/sh\n/bin/cat > \"$PROMPTCAT_TEST_CLIPBOARD\"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", backend)
	capture := filepath.Join(t.TempDir(), "clipboard.txt")
	t.Setenv("PROMPTCAT_TEST_CLIPBOARD", capture)
	for _, args := range [][]string{
		{"clipboard", ".", "--ignore-dir=generated"},
		{"clipboard", root, "--ignore-dir=generated"},
		{"clipboard", "--ignore-dir=generated"},
		{"clipboard", "src", "notes.txt", "--exclude=ts"},
	} {
		var stdout, stderr bytes.Buffer
		if err := run(args, &stdout, &stderr); err != nil {
			t.Fatalf("%q: %v", args, err)
		}
		data, err := os.ReadFile(capture)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if !strings.Contains(text, "package main") || !strings.Contains(text, "<<<FILE:") {
			t.Errorf("missing formatted source for %q: %s", args, text)
		}
		if strings.Contains(text, "noise.go") || strings.Contains(text, "large.go") || strings.Contains(text, "binary.go") {
			t.Errorf("unsafe selection for %q", args)
		}
		if strings.Contains(text, "TOKEN=hidden") || strings.Contains(text, "<svg>") {
			t.Errorf("directory selection included sensitive or media files for %q", args)
		}
		if stdout.Len() != 0 || !strings.Contains(stderr.String(), "Copied Promptcat output") {
			t.Errorf("unexpected command output: %q / %q", &stdout, &stderr)
		}
		if args[1] == "src" && (!strings.Contains(text, "explicit file") || strings.Contains(text, "app.ts")) {
			t.Errorf("mixed inputs or extension filter lost: %s", text)
		}
	}
}

func TestClipboardExplicitInputsKeepDefaults(t *testing.T) {
	root := t.TempDir()
	writeAutoFiles(t, root, map[string]string{"notes.txt": "notes", "main.go": "package main\n"})
	t.Chdir(root)
	opts := options{inputs: []string{"notes.txt", "*.go"}}
	paths, err := expandClipboardInputs(&opts, io.Discard)
	if err != nil || len(paths) != 2 || opts.maxSize != 0 || opts.ignoredDirs != nil {
		t.Fatalf("explicit input behavior changed: %v, %#v, %v", paths, opts, err)
	}
	opts = options{inputs: []string{"."}, maxSize: 123}
	if _, err := expandClipboardInputs(&opts, io.Discard); err != nil || opts.maxSize != 123 {
		t.Fatalf("directory size override lost: %#v, %v", opts, err)
	}
}

func TestClipboardDirectoryHonorsGlobExclusions(t *testing.T) {
	root := t.TempDir()
	writeAutoFiles(t, root, map[string]string{"src/keep.go": "package keep\n", "src/drop.go": "package drop\n"})
	t.Chdir(root)
	opts := options{inputs: []string{".", "src/keep.go"}, excludePatterns: []string{"**/drop.go"}}
	paths, err := expandClipboardInputs(&opts, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != filepath.Join("src", "keep.go") {
		t.Fatalf("clipboard selection: %#v", paths)
	}
}
