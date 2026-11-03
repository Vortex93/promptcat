package main

import (
	"io"
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
		{"pack", "--output=review.tar.zst"},
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
