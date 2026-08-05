package main

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestSelectAutoFilesGoProject(t *testing.T) {
	root := t.TempDir()
	writeAutoFiles(t, root, map[string]string{
		"README.md":                     "# Example\n",
		"go.mod":                        "module example.com/project\n",
		"go.sum":                        "ignored\n",
		"cmd/project/main.go":           "package main\n",
		"node_modules/library/index.go": "package library\n",
	})

	assertAutoFiles(t, root, nil, []string{
		"README.md",
		"cmd/project/main.go",
		"go.mod",
	})
}

func TestSelectAutoFilesVueTypeScriptProject(t *testing.T) {
	root := t.TempDir()
	writeAutoFiles(t, root, map[string]string{
		"package.json":      `{"dependencies":{"vue":"^3.0.0"},"devDependencies":{"typescript":"^5.0.0"}}`,
		"package-lock.json": "ignored\n",
		"src/App.vue":       "<template><main /></template>\n",
		"src/main.ts":       "import App from './App.vue'\n",
		"tsconfig.json":     "{}\n",
		"vite.config.ts":    "export default {}\n",
	})

	assertAutoFiles(t, root, nil, []string{
		"package.json",
		"src/App.vue",
		"src/main.ts",
		"tsconfig.json",
		"vite.config.ts",
	})
}

func TestSelectAutoFilesMixedProjectAndCustomIgnore(t *testing.T) {
	root := t.TempDir()
	writeAutoFiles(t, root, map[string]string{
		"Cargo.lock":      "ignored\n",
		"Cargo.toml":      "[package]\nname = 'example'\n",
		"cmd/main.go":     "package main\n",
		"go.mod":          "module example.com/project\n",
		"package.json":    `{}`,
		"src/index.ts":    "export const value = 1\n",
		"src/lib.rs":      "pub fn value() {}\n",
		"target/build.rs": "ignored\n",
	})

	assertAutoFiles(t, root, map[string]bool{"src": true}, []string{
		"Cargo.toml",
		"cmd/main.go",
		"go.mod",
		"package.json",
	})
}

func TestParseArgsAcceptsStandaloneAuto(t *testing.T) {
	opts, err := parseArgs([]string{"auto", "--ignore-dir=generated"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}

	if !opts.auto || !reflect.DeepEqual(opts.ignoredDirs, map[string]bool{"generated": true}) {
		t.Fatalf("unexpected auto options: %#v", opts)
	}

	if _, err := parseArgs([]string{"auto", "main.go"}); err == nil {
		t.Fatal("expected auto with explicit input to fail")
	}
}

func writeAutoFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()

	for relativePath, content := range files {
		path := filepath.Join(root, filepath.FromSlash(relativePath))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
}

func assertAutoFiles(t *testing.T, root string, ignoredDirs map[string]bool, wantRelativePaths []string) {
	t.Helper()

	got, err := selectAutoFiles(root, ignoredDirs)
	if err != nil {
		t.Fatalf("selectAutoFiles returned error: %v", err)
	}

	want := make([]string, 0, len(wantRelativePaths))
	for _, relativePath := range wantRelativePaths {
		want = append(want, filepath.Join(root, filepath.FromSlash(relativePath)))
	}
	sort.Strings(want)

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("selectAutoFiles returned %#v, want %#v", got, want)
	}
}
