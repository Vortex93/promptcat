package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPackProgressRedirectedAndVerbose(t *testing.T) {
	root := t.TempDir()
	writeAutoFiles(t, root, map[string]string{
		"main.go":        "package main\n",
		".env":           "TOKEN=private\n",
		"node_modules/a": "dependency\n",
		"image.svg":      "<svg></svg>\n",
	})
	for _, verbose := range []bool{false, true} {
		output := filepath.Join(t.TempDir(), "archive.zip")
		args := []string{"pack", "--output=" + output, root}
		if verbose {
			args = append(args, "--verbose")
		}
		var stdout, stderr bytes.Buffer
		if err := run(args, &stdout, &stderr); err != nil {
			t.Fatal(err)
		}
		text := stderr.String()
		for _, part := range []string{"Archive created successfully", "Files:      1", "Skipped:    3 paths", "Size:", "Duration:", "Output:"} {
			if !strings.Contains(text, part) {
				t.Errorf("missing %q: %s", part, text)
			}
		}
		if strings.ContainsAny(text, "\x1b\r") || stdout.Len() != 0 {
			t.Errorf("redirected output contains terminal controls or stdout: %q / %q", text, &stdout)
		}
		if strings.Contains(text, "Creating archive") || strings.Contains(text, "%") {
			t.Errorf("redirected output includes a progress bar: %q", text)
		}
		if strings.Contains(text, "Included: main.go") != verbose || strings.Contains(text, "Skipping (sensitive path): .env") != verbose {
			t.Errorf("wrong verbose output: %s", text)
		}
		if len(readTestZIP(t, output)["main.go"]) == 0 {
			t.Fatal("archive missing source")
		}
	}
}

func TestPackProgressFailureAndAnimation(t *testing.T) {
	root := t.TempDir()
	writeAutoFiles(t, root, map[string]string{"main.go": "package main\n"})
	var stderr bytes.Buffer
	err := run([]string{"pack", "--output=" + filepath.Join(t.TempDir(), "missing", "archive.zip"), root}, io.Discard, &stderr)
	if err == nil || strings.Contains(stderr.String(), "Archive created successfully") {
		t.Fatalf("wrong failure output: %v / %s", err, &stderr)
	}

	// Exercise progress redraws and failure cleanup without needing a TTY.
	var animated bytes.Buffer
	progress := &packProgress{writer: &animated, interactive: true, started: time.Now()}
	progress.start(2)
	progress.update()
	progress.update()
	before := animated.Len()
	progress.update()
	if animated.Len() != before {
		t.Fatal("unchanged progress redrew the bar")
	}
	progress.setOperation("Writing navigation")
	progress.fail()
	if !strings.Contains(animated.String(), "0 / 2 files") || !strings.Contains(animated.String(), "50%  1 / 2 files") || !strings.Contains(animated.String(), "100%  2 / 2 files") || !strings.Contains(animated.String(), "Writing navigation") || !strings.HasSuffix(animated.String(), "\r\x1b[2K") {
		t.Fatalf("bar did not update or clean up: %q", &animated)
	}
}

func TestPackProgressOutputFileIsNotTerminal(t *testing.T) {
	file, err := os.Create(filepath.Join(t.TempDir(), "log"))
	if err != nil {
		t.Fatal(err)
	}
	progress := newPackProgress(file, file, false)
	if progress.interactive {
		t.Fatal("regular file must not be treated as a terminal")
	}
	progress.start(1)
	progress.update()
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPackProgressAccountsForLateSkips(t *testing.T) {
	root := t.TempDir()
	writeAutoFiles(t, root, map[string]string{
		"main.go":    "package main\n",
		"changed.go": "binary\x00content",
	})
	var stderr bytes.Buffer
	opts := options{archiveChatGPT: true, archiveAI: true, archiveFiles: true}
	applyArchiveDefaults(&opts)
	progress := &packProgress{writer: &stderr, interactive: true, started: time.Now()}
	err := runChatGPTArchive(opts, root, filepath.Join(t.TempDir(), "archive.zip"), []string{"main.go", "changed.go"}, nil, &stderr, progress)
	if err != nil {
		t.Fatal(err)
	}
	text := stderr.String()
	for _, part := range []string{"100%  2 / 2 files", "Files:      1", "Skipped:    1 paths"} {
		if !strings.Contains(text, part) {
			t.Errorf("missing %q: %q", part, text)
		}
	}
}

func TestVerboseOptionCompletion(t *testing.T) {
	if _, err := parseArgs([]string{"--verbose"}); err == nil {
		t.Fatal("--verbose requires an archive command")
	}
	for _, shell := range supportedAutocompleteShells {
		script, err := autocompleteScript(shell)
		if err != nil || !strings.Contains(script, "verbose") {
			t.Errorf("missing verbose completion for %s: %v", shell, err)
		}
	}
}
