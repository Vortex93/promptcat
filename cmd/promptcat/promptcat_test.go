package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"
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

func TestParseArgsRejectsEmptyOrExclusionOnlyPatterns(t *testing.T) {
	for _, args := range [][]string{{"!"}, {"!**/excluded/**"}} {
		if _, err := parseArgs(args); err == nil {
			t.Fatalf("parseArgs(%#v) returned nil error", args)
		}
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

	want := "<<<FILE: README.md>>>\nline one\nline two\n<<<END FILE>>>\n\n"
	if output.String() != want {
		t.Fatalf("unexpected output:\n%s", output.String())
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
