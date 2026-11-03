package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseArgsPack(t *testing.T) {
	for _, flags := range [][]string{
		{},
		{"."},
		{"--include=**.txt", "--clipboard", "."},
		{"--pattern", "**.go", "--output", "review.zip", "--max-size=2MB", "--exclude-repo=old", "."},
	} {
		got, err := parseArgs(append([]string{"pack"}, flags...))
		if err != nil {
			t.Fatal(err)
		}
		want, err := parseArgs(append([]string{"archive", "--chatgpt"}, flags...))
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("pack %q differs from archive --chatgpt: %#v != %#v (%v)", flags, got, want, err)
		}
	}
	for _, args := range [][]string{
		{"pack", "one", "two"},
		{"pack", "auto"},
		{"pack", "update"},
	} {
		if _, err := parseArgs(args); err == nil {
			t.Errorf("expected error for %q", args)
		}
	}
	// Archive patterns must also work when flags precede the command.
	before, err := parseArgs([]string{"--include=**.txt", "pack", "."})
	if err != nil || !reflect.DeepEqual(before.archiveIncludes, []string{"**.txt"}) {
		t.Fatalf("include before pack: %#v, %v", before, err)
	}
	literal, err := parseArgs([]string{"--", "pack"})
	if err != nil || literal.archive || !reflect.DeepEqual(literal.inputs, []string{"pack"}) {
		t.Fatalf("literal pack file: %#v, %v", literal, err)
	}
	for _, shell := range supportedAutocompleteShells {
		script, err := autocompleteScript(shell)
		if err != nil || !strings.Contains(script, "pack") {
			t.Errorf("missing pack completion for %s: %v", shell, err)
		}
	}
}

func TestPackOutputPaths(t *testing.T) {
	workspace := t.TempDir()
	root := filepath.Join(workspace, "webmail")
	frontend := filepath.Join(root, "frontend")
	downloads := filepath.Join(workspace, "Downloads")
	for _, directory := range []string{frontend, downloads, filepath.Join(root, "outdir")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeAutoFiles(t, root, map[string]string{"main.go": "package main\n", "frontend/app.ts": "export const app = 1\n"})
	t.Setenv("HOME", workspace)
	t.Chdir(root)
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{"default", []string{"pack", "."}, filepath.Join(root, "archive.zip")},
		{"relative directory", []string{"pack", ".", "--output=outdir"}, filepath.Join(root, "outdir", "webmail.zip")},
		{"relative root directory", []string{"pack", ".", "--output=."}, filepath.Join(root, "webmail.zip")},
		{"absolute directory", []string{"pack", ".", "--output=" + downloads}, filepath.Join(downloads, "webmail.zip")},
		{"chatgpt directory", []string{"archive", "--chatgpt", ".", "--output=" + downloads}, filepath.Join(downloads, "webmail.zip")},
		{"home directory", []string{"pack", "./frontend", "--output=~/Downloads/"}, filepath.Join(downloads, "frontend.zip")},
		{"absolute filename", []string{"pack", ".", "--output=" + filepath.Join(downloads, "custom.ZIP")}, filepath.Join(downloads, "custom.ZIP")},
		{"relative filename", []string{"pack", "./frontend", "--output=custom.zip"}, filepath.Join(frontend, "custom.zip")},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := run(test.args, io.Discard, io.Discard); err != nil {
				t.Fatal(err)
			}
			entries := readTestZIP(t, test.want)
			if len(entries["START_HERE.md"]) == 0 {
				t.Fatal("missing archive guide")
			}
		})
	}
}

func TestPackOutputValidationAndOverwrite(t *testing.T) {
	root := t.TempDir()
	writeAutoFiles(t, root, map[string]string{"main.go": "package main\n"})
	output := filepath.Join(root, filepath.Base(root)+".zip")
	if err := os.WriteFile(output, []byte("old archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"pack", root, "--output=" + root}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	entries := readTestZIP(t, output)
	if _, ok := entries[filepath.Base(output)]; ok {
		t.Fatal("archive included its previous output")
	}
	if len(entries["main.go"]) == 0 {
		t.Fatal("existing archive was not replaced")
	}

	for _, path := range []string{filepath.Join(root, "missing"), filepath.Join(root, "missing.zip", "child.zip"), filepath.Join(root, "wrong.tar")} {
		if err := run([]string{"pack", root, "--output=" + path}, io.Discard, io.Discard); err == nil {
			t.Errorf("accepted invalid output %s", path)
		}
	}
	link := filepath.Join(root, "link.zip")
	if err := os.Symlink(output, link); err == nil {
		if err := run([]string{"pack", root, "--output=" + link}, io.Discard, io.Discard); err == nil {
			t.Error("accepted symlink as ZIP output")
		}
	}
	if _, err := resolveArchiveOutput(string(filepath.Separator), root, true); err == nil {
		t.Error("accepted filesystem root without an explicit filename")
	}
	before, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeZIP(output, func(*zipEntries) error { return io.ErrUnexpectedEOF }); err == nil {
		t.Fatal("expected failed ZIP creation")
	}
	after, err := os.ReadFile(output)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("failed export changed the previous ZIP: %v", err)
	}
}

func TestPackCurrentDirectory(t *testing.T) {
	root := t.TempDir()
	writeAutoFiles(t, root, map[string]string{"main.go": "package main\n"})
	t.Chdir(root)
	if err := run([]string{"pack", "."}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	entries := readTestZIP(t, filepath.Join(root, "archive.zip"))
	for _, name := range []string{"main.go", "START_HERE.md", ".promptcat/files.json", ".promptcat/tools.py"} {
		if len(entries[name]) == 0 {
			t.Errorf("missing ZIP entry: %s", name)
		}
	}
}

func TestPackDeploymentFiles(t *testing.T) {
	root := t.TempDir()
	writeAutoFiles(t, root, map[string]string{
		"deploy/Caddyfile":             "example configuration\n",
		"deploy/Caddyfile.example":     "example configuration\n",
		"deploy/Caddyfile.public":      "public configuration\n",
		"deploy/Caddyfile.vpn.snippet": "vpn configuration\n",
		"deploy/public.env.example":    "EXAMPLE=value\n",
		"deploy/vpn.env.sample":        "EXAMPLE=value\n",
		"deploy/.env.example":          "EXAMPLE=value\n",
		"deploy/.env":                  "SECRET=private\n",
		"deploy/public.env":            "SECRET=private\n",
		"deploy/compose.public.yml":    "services: {}\n",
		"deploy/README.md":             "deployment instructions\n",
	})
	if err := run([]string{"pack", root}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	entries := readTestZIP(t, filepath.Join(root, "archive.zip"))
	for _, name := range []string{
		"deploy/Caddyfile", "deploy/Caddyfile.example", "deploy/Caddyfile.public",
		"deploy/Caddyfile.vpn.snippet", "deploy/public.env.example", "deploy/vpn.env.sample",
		"deploy/.env.example", "deploy/compose.public.yml", "deploy/README.md",
	} {
		if len(entries[name]) == 0 {
			t.Errorf("missing deployment file: %s", name)
		}
	}
	for _, name := range []string{"deploy/.env", "deploy/public.env"} {
		if _, ok := entries[name]; ok {
			t.Errorf("unexpected live environment file: %s", name)
		}
	}
}
