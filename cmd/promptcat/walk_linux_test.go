//go:build linux

package main

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestWalkDirUnsortedDeepTreeWithLowFDLimit(t *testing.T) {
	var original syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &original); err != nil {
		t.Fatal(err)
	}
	if original.Cur < 64 {
		t.Skip("FD limit already unusually low")
	}
	limited := original
	limited.Cur = 64
	if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &limited); err != nil {
		t.Skipf("cannot lower FD limit: %v", err)
	}
	defer syscall.Setrlimit(syscall.RLIMIT_NOFILE, &original)

	root := t.TempDir()
	current := root
	for i := 0; i < 100; i++ {
		current = filepath.Join(current, "deep")
		if err := os.Mkdir(current, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	leaf := filepath.Join(current, "leaf.txt")
	if err := os.WriteFile(leaf, []byte("text"), 0o600); err != nil {
		t.Fatal(err)
	}

	found := false
	err := walkDirUnsorted(root, func(path string, entry fs.DirEntry) error {
		if path == leaf {
			found = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("deep walk failed: %v", err)
	}
	if !found {
		t.Fatal("deep leaf was not visited")
	}
}

func TestRunSkipsFIFO(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "input.fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		var output, stderr bytes.Buffer
		done <- run([]string{fifo}, &output, &stderr)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run blocked while opening FIFO")
	}

	var output, stderr bytes.Buffer
	if err := run([]string{fifo}, &output, &stderr); err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 || !strings.Contains(stderr.String(), "Skipping (not regular)") {
		t.Fatalf("FIFO was not skipped: output=%q stderr=%q", output.String(), stderr.String())
	}
}
