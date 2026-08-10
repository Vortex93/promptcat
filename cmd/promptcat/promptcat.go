package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var version = "0.1.1"
var buildDate = "dev"

var binaryExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true,
	".bmp": true, ".ico": true, ".pdf": true, ".zip": true, ".gz": true,
	".tar": true, ".7z": true, ".rar": true, ".exe": true, ".dll": true,
	".so": true, ".bin": true, ".woff": true, ".woff2": true, ".ttf": true,
	".otf": true, ".mp3": true, ".mp4": true, ".mov": true, ".avi": true,
	".mkv": true, ".webm": true,
}

const (
	fileStartMarkerPrefix = "<<<FILE: "
	fileStartMarkerSuffix = ">>>"
	fileEndMarker         = "<<<END FILE>>>"
)

var errBinaryContent = errors.New("binary content")

func parseExts(exts string) map[string]bool {
	if exts == "" {
		return nil
	}

	set := map[string]bool{}

	for _, e := range strings.Split(exts, ",") {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}

		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}

		set[strings.ToLower(e)] = true
	}

	return set
}

func parseDirs(s string) map[string]bool {
	if s == "" {
		return nil
	}

	set := map[string]bool{}

	for _, d := range strings.Split(s, ",") {
		d = strings.TrimSpace(d)
		if d != "" {
			set[strings.ToLower(d)] = true
		}
	}

	return set
}

func isIgnored(path string, ignored map[string]bool) bool {
	if ignored == nil {
		return false
	}

	parts := strings.Split(filepath.ToSlash(path), "/")

	for _, p := range parts {
		if ignored[strings.ToLower(p)] {
			return true
		}
	}

	return false
}

func isProbablyText(data []byte) bool {
	if len(data) == 0 {
		return true
	}

	sample := data
	if len(sample) > 8000 {
		sample = sample[:8000]
	}

	suspicious := 0

	for _, b := range sample {
		if b == 0 {
			return false
		}

		if b < 32 && b != 9 && b != 10 && b != 13 && b != 12 && b != 8 {
			suspicious++
		}
	}

	return float64(suspicious)/float64(len(sample)) < 0.02
}

type options struct {
	auto            bool
	fullPath        bool
	include         map[string]bool
	exclude         map[string]bool
	ignoredDirs     map[string]bool
	inputs          []string
	excludePatterns []string
}

func parseArgs(args []string) (options, error) {
	var opts options

	for i := 0; i < len(args); i++ {
		arg := args[i]

		switch {
		case arg == "-h" || arg == "--help" || arg == "help":
			fmt.Print(usage())
			os.Exit(0)

		case arg == "-v" || arg == "--version":
			fmt.Printf("promptcat %s (%s)\n", version, buildDate)
			os.Exit(0)

		case arg == "auto":
			opts.auto = true

		case arg == "--fullpath" || arg == "fullpath":
			opts.fullPath = true

		case arg == "--include":
			i++
			if i >= len(args) {
				return opts, flagError("missing value for --include")
			}
			opts.include = parseExts(args[i])

		case arg == "--exclude":
			i++
			if i >= len(args) {
				return opts, flagError("missing value for --exclude")
			}
			opts.exclude = parseExts(args[i])

		case arg == "--ignore-dir":
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				opts.ignoredDirs = parseDirs(args[i])
			}

		case strings.HasPrefix(arg, "--include="):
			opts.include = parseExts(strings.TrimPrefix(arg, "--include="))

		case strings.HasPrefix(arg, "include="):
			opts.include = parseExts(strings.TrimPrefix(arg, "include="))

		case strings.HasPrefix(arg, "--exclude="):
			opts.exclude = parseExts(strings.TrimPrefix(arg, "--exclude="))

		case strings.HasPrefix(arg, "exclude="):
			opts.exclude = parseExts(strings.TrimPrefix(arg, "exclude="))

		case strings.HasPrefix(arg, "--ignore-dir="):
			opts.ignoredDirs = parseDirs(strings.TrimPrefix(arg, "--ignore-dir="))

		case strings.HasPrefix(arg, "ignore-dir="):
			opts.ignoredDirs = parseDirs(strings.TrimPrefix(arg, "ignore-dir="))

		case strings.HasPrefix(arg, "--fullpath="):
			value, err := strconv.ParseBool(strings.TrimPrefix(arg, "--fullpath="))
			if err != nil {
				return opts, flagError("invalid value for --fullpath")
			}
			opts.fullPath = value

		case strings.HasPrefix(arg, "fullpath="):
			value, err := strconv.ParseBool(strings.TrimPrefix(arg, "fullpath="))
			if err != nil {
				return opts, flagError("invalid value for fullpath")
			}
			opts.fullPath = value

		case strings.HasPrefix(arg, "!"):
			pattern := strings.TrimPrefix(arg, "!")
			if pattern == "" {
				return opts, flagError("empty exclusion pattern")
			}
			if _, err := globToRegex(pattern); err != nil {
				return opts, flagError(fmt.Sprintf("invalid exclusion pattern %q: %v", pattern, err))
			}
			opts.excludePatterns = append(opts.excludePatterns, pattern)

		default:
			opts.inputs = append(opts.inputs, arg)
		}
	}

	if opts.auto && len(opts.inputs) > 0 {
		return opts, flagError("auto cannot be combined with explicit files or glob patterns")
	}
	if !opts.auto && len(opts.inputs) == 0 && len(opts.excludePatterns) > 0 {
		return opts, flagError("at least one file path or glob pattern is required")
	}

	return opts, nil
}

func flagError(message string) error {
	return fmt.Errorf("%s", message)
}

func usage() string {
	return `promptcat - concatenate text and source files for AI prompts

Usage:
  promptcat [options] <files...>
  promptcat auto [options]

Options:
  --help, -h            Show help
  --version, -v         Show version
  --fullpath            Output absolute file paths
  --include=go,md       Include only specific extensions
  --exclude=json        Exclude extensions
	--ignore-dir=name     Ignore directories by name
  !pattern              Exclude files matching a glob pattern

Output format:
  <<<FILE: path/to/file>>>
  <file contents>
  <<<END FILE>>>

Examples:
  promptcat "cmd/**/*.go"
  promptcat "**/*.md" "!**/excluded/*.md"
  promptcat --include=go,md --ignore-dir=.git,node_modules "**/*"
  promptcat auto
`
}

func hasGlob(pattern string) bool {
	return strings.ContainsAny(pattern, "*?[")
}

func trimDotSlash(path string) string {
	path = filepath.ToSlash(path)
	return strings.TrimPrefix(path, "./")
}

func globToRegex(pattern string) (*regexp.Regexp, error) {
	var builder strings.Builder
	builder.WriteString("^")

	pattern = trimDotSlash(pattern)

	for i := 0; i < len(pattern); i++ {
		char := pattern[i]

		if char == '*' {
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				if i+2 < len(pattern) && pattern[i+2] == '/' {
					builder.WriteString("(?:[^/]+/)*")
					i += 2
					continue
				}

				builder.WriteString(".*")
				i++
				continue
			}

			builder.WriteString("[^/]*")
			continue
		}

		switch char {
		case '?':
			builder.WriteString("[^/]")
		case '[':
			end := strings.IndexByte(pattern[i+1:], ']')
			if end < 0 {
				return nil, fmt.Errorf("unterminated character class")
			}

			end += i + 1
			characterClass := pattern[i+1 : end]
			if characterClass == "" {
				return nil, fmt.Errorf("empty character class")
			}

			builder.WriteByte('[')
			if characterClass[0] == '!' {
				builder.WriteByte('^')
				characterClass = characterClass[1:]
			}
			if characterClass == "" {
				return nil, fmt.Errorf("empty character class")
			}
			builder.WriteString(characterClass)
			builder.WriteByte(']')
			i = end
		case '.', '(', ')', '+', '|', '^', '$', '{', '}', ']', '\\':
			builder.WriteByte('\\')
			builder.WriteByte(char)
		default:
			builder.WriteByte(char)
		}
	}

	builder.WriteString("$")

	return regexp.Compile(builder.String())
}

func globRoot(pattern string) string {
	slashPattern := filepath.ToSlash(pattern)
	rootPrefix := ""
	if filepath.IsAbs(pattern) {
		rootPrefix = filepath.ToSlash(filepath.VolumeName(pattern)) + "/"
		slashPattern = strings.TrimPrefix(slashPattern, rootPrefix)
	}

	parts := strings.Split(slashPattern, "/")
	rootParts := make([]string, 0, len(parts))

	for _, part := range parts {
		if strings.ContainsAny(part, "*?[") {
			break
		}
		if part != "" {
			rootParts = append(rootParts, part)
		}
	}

	if len(rootParts) == 0 {
		if rootPrefix != "" {
			return filepath.FromSlash(rootPrefix)
		}

		return "."
	}

	root := filepath.FromSlash(strings.Join(rootParts, "/"))
	if rootPrefix == "" {
		return root
	}

	return filepath.Join(filepath.FromSlash(rootPrefix), root)
}

func expandInput(input string, ignoredDirs map[string]bool) []string {
	if !hasGlob(input) {
		return []string{input}
	}

	matches, _ := expandInputs([]string{input}, nil, ignoredDirs)
	return matches
}

func expandInputs(inputs, excludePatterns []string, ignoredDirs map[string]bool) ([]string, error) {
	excludeMatchers := make([]*regexp.Regexp, 0, len(excludePatterns))
	for _, pattern := range excludePatterns {
		matcher, err := globToRegex(pattern)
		if err != nil {
			return nil, err
		}
		excludeMatchers = append(excludeMatchers, matcher)
	}

	type globInput struct {
		input   string
		matcher *regexp.Regexp
		root    string
	}

	globInputs := make([]globInput, 0, len(inputs))
	for _, input := range inputs {
		if !hasGlob(input) {
			continue
		}

		matcher, err := globToRegex(input)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Skipping (bad pattern): %s\n", input)
			continue
		}
		globInputs = append(globInputs, globInput{input: input, matcher: matcher, root: globRoot(input)})
	}

	matchesByInput := make(map[string][]string, len(globInputs))
	byRoot := make(map[string][]int)
	for i, input := range globInputs {
		byRoot[input.root] = append(byRoot[input.root], i)
	}

	for root, indexes := range byRoot {
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			for _, index := range indexes {
				fmt.Fprintf(os.Stderr, "Skipping (glob root not found): %s\n", globInputs[index].input)
			}
			continue
		}

		err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if entry.IsDir() {
				if ignoredDirs != nil && ignoredDirs[strings.ToLower(entry.Name())] {
					return filepath.SkipDir
				}
				return nil
			}

			trimmedPath := trimDotSlash(path)
			for _, index := range indexes {
				if globInputs[index].matcher.MatchString(trimmedPath) {
					input := globInputs[index].input
					matchesByInput[input] = append(matchesByInput[input], path)
				}
			}
			return nil
		})
		if err != nil {
			for _, index := range indexes {
				fmt.Fprintf(os.Stderr, "Skipping (walk error): %s\n", globInputs[index].input)
			}
		}
	}

	expanded := make([]string, 0, len(inputs))
	seen := map[string]bool{}
	appendMatch := func(match string) {
		if matchesAnyPattern(trimDotSlash(filepath.ToSlash(match)), excludeMatchers) || seen[match] {
			return
		}
		seen[match] = true
		expanded = append(expanded, match)
	}
	for _, input := range inputs {
		if !hasGlob(input) {
			appendMatch(input)
			continue
		}
		matches := matchesByInput[input]
		sort.Strings(matches)
		for _, match := range matches {
			appendMatch(match)
		}
	}

	return expanded, nil
}

func matchesAnyPattern(path string, matchers []*regexp.Regexp) bool {
	for _, matcher := range matchers {
		if matcher.MatchString(path) {
			return true
		}
	}

	return false
}

type trimTrailingNewlinesWriter struct {
	writer  io.Writer
	pending int
}

func (w *trimTrailingNewlinesWriter) Write(data []byte) (int, error) {
	start := 0
	for i, b := range data {
		if b == '\n' {
			if start < i {
				if err := w.flush(data[start:i]); err != nil {
					return 0, err
				}
			}
			w.pending++
			start = i + 1
			continue
		}

		if w.pending > 0 {
			if err := w.flushNewlines(); err != nil {
				return 0, err
			}
		}
	}
	if w.pending == 0 && start < len(data) {
		if err := w.flush(data[start:]); err != nil {
			return 0, err
		}
	}
	return len(data), nil
}

func (w *trimTrailingNewlinesWriter) flush(data []byte) error {
	n, err := w.writer.Write(data)
	if err != nil {
		return err
	}
	if n != len(data) {
		return io.ErrShortWrite
	}
	return nil
}

func (w *trimTrailingNewlinesWriter) flushNewlines() error {
	var newlines [256]byte
	for i := range newlines {
		newlines[i] = '\n'
	}
	for w.pending > 0 {
		n := w.pending
		if n > len(newlines) {
			n = len(newlines)
		}
		if err := w.flush(newlines[:n]); err != nil {
			return err
		}
		w.pending -= n
	}
	return nil
}

func (w *trimTrailingNewlinesWriter) finish() error {
	return w.flush([]byte("\n"))
}

func writeFileBlock(output io.Writer, path string, data []byte) error {
	if _, err := fmt.Fprintf(output, "%s%s%s\n", fileStartMarkerPrefix, filepath.ToSlash(path), fileStartMarkerSuffix); err != nil {
		return err
	}
	content := &trimTrailingNewlinesWriter{writer: output}
	if _, err := content.Write(data); err != nil {
		return err
	}
	if err := content.finish(); err != nil {
		return err
	}
	_, err := fmt.Fprintf(output, "%s\n\n", fileEndMarker)
	return err
}

func writeFileBlockFromFile(output io.Writer, path string, file *os.File) error {
	const sampleSize = 8000
	sample := make([]byte, sampleSize)
	n, err := io.ReadFull(file, sample)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return err
	}
	if !isProbablyText(sample[:n]) {
		return errBinaryContent
	}
	if _, err := fmt.Fprintf(output, "%s%s%s\n", fileStartMarkerPrefix, filepath.ToSlash(path), fileStartMarkerSuffix); err != nil {
		return err
	}

	content := &trimTrailingNewlinesWriter{writer: output}
	if _, err := content.Write(sample[:n]); err != nil {
		return err
	}
	if _, err := io.Copy(content, file); err != nil {
		return err
	}
	if err := content.finish(); err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "%s\n\n", fileEndMarker)
	return err
}

func main() {
	opts, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, usage())
		os.Exit(1)
	}

	var args []string
	if opts.auto {
		args, err = selectAutoFiles(".", opts.ignoredDirs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Auto detection failed: %v\n", err)
			os.Exit(1)
		}
	} else {
		args, err = expandInputs(opts.inputs, opts.excludePatterns, opts.ignoredDirs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to expand inputs: %v\n", err)
			os.Exit(1)
		}
	}

	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, usage())
		os.Exit(1)
	}

	output := bufio.NewWriter(os.Stdout)
	defer output.Flush()

	for _, input := range args {
		if isIgnored(input, opts.ignoredDirs) {
			fmt.Fprintf(os.Stderr, "Skipping (ignored dir): %s\n", input)
			continue
		}

		info, err := os.Stat(input)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Skipping (not found): %s\n", input)
			continue
		}

		if info.IsDir() {
			fmt.Fprintf(os.Stderr, "Skipping (directory): %s\n", input)
			continue
		}

		ext := strings.ToLower(filepath.Ext(input))

		if opts.include != nil && !opts.include[ext] {
			fmt.Fprintf(os.Stderr, "Skipping (not included): %s\n", input)
			continue
		}

		if opts.exclude != nil && opts.exclude[ext] {
			fmt.Fprintf(os.Stderr, "Skipping (excluded ext): %s\n", input)
			continue
		}

		if binaryExtensions[ext] {
			fmt.Fprintf(os.Stderr, "Skipping (binary extension): %s\n", input)
			continue
		}

		file, err := os.Open(input)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Skipping (read error): %s\n", input)
			continue
		}

		path := input
		if opts.fullPath {
			abs, err := filepath.Abs(input)
			if err == nil {
				path = abs
			}
		}

		err = writeFileBlockFromFile(output, path, file)
		file.Close()
		if err != nil {
			if errors.Is(err, errBinaryContent) {
				fmt.Fprintf(os.Stderr, "Skipping (binary content): %s\n", input)
			} else {
				fmt.Fprintf(os.Stderr, "Skipping (read error): %s\n", input)
			}
		}
	}
}
