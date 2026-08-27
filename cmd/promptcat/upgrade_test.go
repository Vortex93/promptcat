package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"runtime"
	"testing"
)

func TestParseArgsAcceptsUpgradeAlone(t *testing.T) {
	opts, err := parseArgs([]string{"--upgrade"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	if !opts.upgrade {
		t.Fatal("expected upgrade to be enabled")
	}

	if _, err := parseArgs([]string{"--upgrade", "README.md"}); err == nil {
		t.Fatal("expected upgrade with input to fail")
	}
}

func TestCompareVersions(t *testing.T) {
	for _, tc := range []struct {
		current string
		latest  string
		want    int
	}{
		{"0.1.0", "0.2.0", -1},
		{"0.2.0", "0.2.0", 0},
		{"0.3.0", "0.2.0", 1},
		{"1.0.0", "0.99.99", 1},
		{"1.2.3", "2.0.0", -1},
	} {
		got, err := compareVersions(tc.current, tc.latest)
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Fatalf("compareVersions(%q, %q) = %d, want %d", tc.current, tc.latest, got, tc.want)
		}
	}
}

func TestExtractBinaryFromTarGz(t *testing.T) {
	binaryName := "promptcat"
	if runtime.GOOS == "windows" {
		binaryName = "promptcat.exe"
	}
	want := []byte("binary")

	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: "promptcat_0.1.2/" + binaryName, Mode: 0o755, Size: int64(len(want)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(want); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := extractBinary(archive.Bytes(), "promptcat_0.1.2_linux_amd64.tar.gz")
	if err != nil {
		t.Fatalf("extractBinary returned error: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("extractBinary = %q, want %q", got, want)
	}
}

func TestVerifyChecksum(t *testing.T) {
	data := []byte("release archive")
	digest := sha256.Sum256(data)
	checksums := []byte(fmt.Sprintf("%s  promptcat_0.1.2_linux_amd64.tar.gz\n", hex.EncodeToString(digest[:])))

	if err := verifyChecksum(data, "promptcat_0.1.2_linux_amd64.tar.gz", checksums); err != nil {
		t.Fatalf("verifyChecksum returned error: %v", err)
	}
	if err := verifyChecksum([]byte("tampered"), "promptcat_0.1.2_linux_amd64.tar.gz", checksums); err == nil {
		t.Fatal("expected checksum mismatch")
	}
}
