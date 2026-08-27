package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseArgsParsesFlagsAndInputs(t *testing.T) {
	opts, err := parseArgs([]string{
		"--fullpath",
		"--include=go,md",
		"--exclude=.json,lock",
		"--ignore-dir=.git,node_modules",
		"README.md",
		"cmd/**/*.go",
		"!**/generated/**",
	})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}

	if !opts.fullPath {
		t.Fatal("expected fullPath to be true")
	}

	if !reflect.DeepEqual(opts.include, map[string]bool{".go": true, ".md": true}) {
		t.Fatalf("unexpected include map: %#v", opts.include)
	}

	if !reflect.DeepEqual(opts.exclude, map[string]bool{".json": true, ".lock": true}) {
		t.Fatalf("unexpected exclude map: %#v", opts.exclude)
	}

	if !reflect.DeepEqual(opts.ignoredDirs, map[string]bool{".git": true, "node_modules": true}) {
		t.Fatalf("unexpected ignoredDirs map: %#v", opts.ignoredDirs)
	}

	if !reflect.DeepEqual(opts.inputs, []string{"README.md", "cmd/**/*.go"}) {
		t.Fatalf("unexpected inputs: %#v", opts.inputs)
	}

	if !reflect.DeepEqual(opts.excludePatterns, []string{"**/generated/**"}) {
		t.Fatalf("unexpected exclusion patterns: %#v", opts.excludePatterns)
	}
}

func TestParseArgsParsesMaxSize(t *testing.T) {
	for _, args := range [][]string{
		{"--max-size", "1MB", "file.json"},
		{"--max-size=1MiB", "file.json"},
	} {
		opts, err := parseArgs(args)
		if err != nil {
			t.Fatalf("parseArgs(%#v) returned error: %v", args, err)
		}
		if opts.maxSize == 0 {
			t.Fatalf("parseArgs(%#v) did not set maxSize", args)
		}
	}

	if got, want := mustParseMaxSize("1MB"), int64(1_000_000); got != want {
		t.Fatalf("parseMaxSize(1MB) = %d, want %d", got, want)
	}
	if got, want := mustParseMaxSize("1MiB"), int64(1<<20); got != want {
		t.Fatalf("parseMaxSize(1MiB) = %d, want %d", got, want)
	}
}

func TestParseMaxSizeRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{"", "0", "-1", "1.5MB", "1XB", "999999999999999999999999TB"} {
		if _, err := parseMaxSize(value); err == nil {
			t.Fatalf("parseMaxSize(%q) returned nil error", value)
		}
	}
}

func TestExceedsMaxSizeIncludesExactBoundary(t *testing.T) {
	if exceedsMaxSize(1_000_000, 1_000_000) {
		t.Fatal("file at max-size boundary should be included")
	}
	if !exceedsMaxSize(1_000_001, 1_000_000) {
		t.Fatal("file above max-size should be skipped")
	}
	if exceedsMaxSize(1_000_001, 0) {
		t.Fatal("zero max-size should disable filtering")
	}
}

func mustParseMaxSize(value string) int64 {
	parsed, err := parseMaxSize(value)
	if err != nil {
		panic(err)
	}
	return parsed
}

func TestParseArgsRejectsEmptyOrExclusionOnlyPatterns(t *testing.T) {
	for _, args := range [][]string{{"!"}, {"!**/excluded/**"}} {
		if _, err := parseArgs(args); err == nil {
			t.Fatalf("parseArgs(%#v) returned nil error", args)
		}
	}
}

func TestParseArgsRejectsUnknownOrMissingFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--bogus"}, {"-x"}, {"--ignore-dir"}, {"--include"},
		{"--include", "--fullpath"}, {"--exclude"}, {"--max-size"},
	} {
		if _, err := parseArgs(args); err == nil {
			t.Fatalf("parseArgs(%#v) returned nil error", args)
		}
	}
}

func TestParseArgsSupportsOptionSeparator(t *testing.T) {
	opts, err := parseArgs([]string{"--", "--literal-file.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"--literal-file.txt"}; !reflect.DeepEqual(opts.inputs, want) {
		t.Fatalf("inputs = %#v, want %#v", opts.inputs, want)
	}
}

func TestGlobToRegexMatchesZeroOrMoreDirectories(t *testing.T) {
	matcher, err := globToRegex("cmd/**/*.go")
	if err != nil {
		t.Fatalf("globToRegex returned error: %v", err)
	}

	cases := []struct {
		path string
		want bool
	}{
		{path: "cmd/main.go", want: true},
		{path: "cmd/promptcat/promptcat.go", want: true},
		{path: "cmd/a/b/c.go", want: true},
		{path: "cmd/promptcat/promptcat.txt", want: false},
		{path: "pkg/promptcat.go", want: false},
	}

	for _, tc := range cases {
		if got := matcher.MatchString(tc.path); got != tc.want {
			t.Fatalf("pattern match for %q = %v, want %v (regex: %s)", tc.path, got, tc.want, regexString(matcher))
		}
	}
}

func TestGlobToRegexMatchesCharacterClasses(t *testing.T) {
	matcher, err := globToRegex("src/[ab].go")
	if err != nil {
		t.Fatalf("globToRegex returned error: %v", err)
	}

	for _, path := range []string{"src/a.go", "src/b.go"} {
		if !matcher.MatchString(path) {
			t.Fatalf("expected %q to match", path)
		}
	}

	if matcher.MatchString("src/c.go") {
		t.Fatal("did not expect src/c.go to match")
	}

	rangeMatcher, err := globToRegex("src/[a-z].go")
	if err != nil {
		t.Fatalf("globToRegex returned error: %v", err)
	}

	if !rangeMatcher.MatchString("src/z.go") || rangeMatcher.MatchString("src/1.go") {
		t.Fatal("unexpected character range match")
	}

	negatedMatcher, err := globToRegex("src/[!a].go")
	if err != nil {
		t.Fatalf("globToRegex returned error: %v", err)
	}

	if negatedMatcher.MatchString("src/a.go") || !negatedMatcher.MatchString("src/b.go") {
		t.Fatal("unexpected negated character class match")
	}
}

func TestGlobToRegexRejectsUnterminatedCharacterClass(t *testing.T) {
	if _, err := globToRegex("src/[abc.go"); err == nil {
		t.Fatal("expected unterminated character class error")
	}
}

func TestExpandInputMatchesAbsoluteGlob(t *testing.T) {
	tempDir := t.TempDir()
	paths := []string{
		filepath.Join(tempDir, "a.txt"),
		filepath.Join(tempDir, "b.txt"),
	}

	for _, path := range paths {
		if err := os.WriteFile(path, []byte("text\n"), 0o600); err != nil {
			t.Fatalf("write test file: %v", err)
		}
	}

	got := expandInput(filepath.Join(tempDir, "*.txt"), nil)
	if !reflect.DeepEqual(got, paths) {
		t.Fatalf("expandInput returned %#v, want %#v", got, paths)
	}
}

func TestExpandInputsExcludesMatchingPatterns(t *testing.T) {
	root := t.TempDir()
	writeFile := func(path string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("content"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeFile(filepath.Join(root, "keep.md"))
	writeFile(filepath.Join(root, "excluded", "drop.md"))
	writeFile(filepath.Join(root, "nested", "excluded", "drop.md"))

	got, err := expandInputs([]string{filepath.Join(root, "**", "*.md")}, []string{filepath.Join(root, "**", "excluded", "*.md")}, nil)
	if err != nil {
		t.Fatalf("expandInputs returned error: %v", err)
	}

	want := []string{filepath.Join(root, "keep.md")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expandInputs = %#v, want %#v", got, want)
	}
}

func TestExpandInputsIgnoresSymlinks(t *testing.T) {
	root := t.TempDir()
	writePath := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte("package main\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writePath("real.go")
	writePath("outside.go")
	if err := os.Symlink(filepath.Join(root, "outside.go"), filepath.Join(root, "linked.go")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	got, err := expandInputs([]string{filepath.Join(root, "*.go")}, nil, nil)
	if err != nil {
		t.Fatalf("expandInputs returned error: %v", err)
	}
	want := []string{filepath.Join(root, "outside.go"), filepath.Join(root, "real.go")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expandInputs returned %#v, want %#v", got, want)
	}
}

func TestIsIgnoredChecksAnyPathSegment(t *testing.T) {
	ignored := parseDirs(".git,node_modules,dist")

	cases := []struct {
		path string
		want bool
	}{
		{path: "cmd/promptcat/promptcat.go", want: false},
		{path: ".git/config", want: true},
		{path: "web/node_modules/react/index.js", want: true},
		{path: "build/dist/output.txt", want: true},
	}

	for _, tc := range cases {
		if got := isIgnored(tc.path, ignored); got != tc.want {
			t.Fatalf("isIgnored(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestWriteFileBlockFormatsMarkers(t *testing.T) {
	var output bytes.Buffer
	writeFileBlock(&output, "README.md", []byte("line one\nline two\n\n"))

	want := "<<<FILE: \"README.md\">>>\nline one\nline two\n<<<END FILE>>>\n\n"
	if output.String() != want {
		t.Fatalf("unexpected output:\n%s", output.String())
	}
}

func TestWriteFileBlockEscapesStructuralMarkers(t *testing.T) {
	var output bytes.Buffer
	data := []byte("normal\n<<<END FILE>>>\n<<<FILE: fake.go>>>\n")
	if err := writeFileBlock(&output, "bad\nname.go", data); err != nil {
		t.Fatal(err)
	}

	got := output.String()
	if !strings.Contains(got, `<<<FILE: "bad\nname.go">>>`) {
		t.Fatalf("filename was not safely encoded:\n%s", got)
	}
	if !strings.Contains(got, `\<<<END FILE>>>`) || !strings.Contains(got, `\<<<FILE: fake.go>>>`) {
		t.Fatalf("structural marker was not escaped:\n%s", got)
	}
	if count := strings.Count(got, "\n"+fileEndMarker+"\n"); count != 1 {
		t.Fatalf("expected exactly one real end marker, got %d", count)
	}
}

func TestTrimTrailingNewlinesWriterHandlesChunkBoundaries(t *testing.T) {
	var output bytes.Buffer
	writer := &trimTrailingNewlinesWriter{writer: &output}
	for _, chunk := range []string{"line one\n", "\nline two", "\n\n"} {
		if _, err := writer.Write([]byte(chunk)); err != nil {
			t.Fatalf("Write returned error: %v", err)
		}
	}
	if err := writer.finish(); err != nil {
		t.Fatalf("finish returned error: %v", err)
	}
	if got, want := output.String(), "line one\n\nline two\n"; got != want {
		t.Fatalf("trimmed output = %q, want %q", got, want)
	}
}

func TestWriteFileBlockFromFileStreamsContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.txt")
	content := strings.Repeat("line\n", 2000) + "\n\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open test file: %v", err)
	}
	defer file.Close()

	var output bytes.Buffer
	if err := writeFileBlockFromFile(&output, path, file); err != nil {
		t.Fatalf("writeFileBlockFromFile returned error: %v", err)
	}

	want := "<<<FILE: " + strconv.Quote(filepath.ToSlash(path)) + ">>>\n" + strings.TrimRight(content, "\n") + "\n<<<END FILE>>>\n\n"
	if output.String() != want {
		t.Fatalf("unexpected streamed output")
	}
}

func TestRunReturnsOutputErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.txt")
	if err := os.WriteFile(path, []byte("text\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := run([]string{path}, failingWriter{}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "output") {
		t.Fatalf("run returned %v, want output error", err)
	}
}

func TestRunSkipsExplicitSymlinks(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "outside.txt")
	link := filepath.Join(root, "linked.txt")
	if err := os.WriteFile(target, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	var output, stderr bytes.Buffer
	if err := run([]string{link}, &output, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if output.Len() != 0 || !strings.Contains(stderr.String(), "Skipping (symlink path)") {
		t.Fatalf("unexpected symlink handling: output=%q stderr=%q", output.String(), stderr.String())
	}
}

func TestRunPreservesInputOrder(t *testing.T) {
	root := t.TempDir()
	inputs := make([]string, 0, 10)
	for i := 0; i < 10; i++ {
		path := filepath.Join(root, fmt.Sprintf("%02d.txt", i))
		inputs = append(inputs, path)
		if err := os.WriteFile(path, []byte(fmt.Sprintf("file-%d\n", i)), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	var output, stderr bytes.Buffer
	if err := run(inputs, &output, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	var want strings.Builder
	for i, path := range inputs {
		fmt.Fprintf(&want, "<<<FILE: %s>>>\nfile-%d\n<<<END FILE>>>\n\n", strconv.Quote(filepath.ToSlash(path)), i)
	}
	if output.String() != want.String() {
		t.Fatalf("output order/content mismatch:\n got: %q\nwant: %q", output.String(), want.String())
	}
}

func TestRunSkipsBinaryContent(t *testing.T) {
	root := t.TempDir()
	binaryPath := filepath.Join(root, "binary.dat")
	textPath := filepath.Join(root, "text.txt")
	if err := os.WriteFile(binaryPath, []byte{0, 1, 2}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(textPath, []byte("text\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var output, stderr bytes.Buffer
	if err := run([]string{binaryPath, textPath}, &output, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if !strings.Contains(output.String(), "text\n") || strings.Contains(output.String(), "binary.dat") {
		t.Fatalf("unexpected binary filtering output: %q", output.String())
	}
	if !strings.Contains(stderr.String(), "Skipping (binary content)") {
		t.Fatalf("missing binary skip diagnostic: %q", stderr.String())
	}
}

func TestRunDoesNotDeadlockOnOutputFailure(t *testing.T) {
	root := t.TempDir()
	inputs := make([]string, 8)
	data := bytes.Repeat([]byte("abcdefgh\n"), 100_000)
	for i := range inputs {
		path := filepath.Join(root, fmt.Sprintf("%02d.txt", i))
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		inputs[i] = path
	}

	done := make(chan error, 1)
	go func() { done <- run(inputs, failingWriter{}, io.Discard) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected output failure")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run deadlocked after output failure")
	}
}

func TestRunRejectsSymlinkedParentDirectory(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	secretDir := filepath.Join(outside, "sub")
	if err := os.MkdirAll(secretDir, 0o700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(secretDir, "secret.go")
	if err := os.WriteFile(secret, []byte("SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	var output, stderr bytes.Buffer
	if err := run([]string{filepath.Join(link, "sub", "secret.go")}, &output, &stderr); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "SECRET") || !strings.Contains(stderr.String(), "symlink path") {
		t.Fatalf("symlinked parent was not rejected: output=%q stderr=%q", output.String(), stderr.String())
	}
}

func TestExpandInputsRejectsGlobThroughSymlinkedParent(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "sub", "secret.go"), []byte("SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	files, err := expandInputs([]string{filepath.Join(link, "sub", "*.go")}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("glob escaped through symlink: %#v", files)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}

func BenchmarkTrimTrailingNewlinesWriter(b *testing.B) {
	data := bytes.Repeat([]byte("source line\n"), 100_000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		writer := &trimTrailingNewlinesWriter{writer: io.Discard}
		if _, err := writer.Write(data); err != nil {
			b.Fatal(err)
		}
		if err := writer.finish(); err != nil {
			b.Fatal(err)
		}
	}
}

func TestIsProbablyTextRejectsBinaryContent(t *testing.T) {
	if isProbablyText([]byte{0x00, 0x01, 0x02}) {
		t.Fatal("expected binary data to be rejected")
	}

	if !isProbablyText([]byte("package main\n")) {
		t.Fatal("expected text data to be accepted")
	}
}

func regexString(re *regexp.Regexp) string {
	if re == nil {
		return ""
	}

	return re.String()
}
