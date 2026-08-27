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

var version = "0.1.2"
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
	upgrade         bool
	fullPath        bool
	maxSize         int64
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

		case arg == "--upgrade":
			opts.upgrade = true

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

		case arg == "--max-size":
			i++
			if i >= len(args) || strings.HasPrefix(args[i], "-") {
				return opts, flagError("missing value for --max-size")
			}
			maxSize, err := parseMaxSize(args[i])
			if err != nil {
				return opts, err
			}
			opts.maxSize = maxSize

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

		case strings.HasPrefix(arg, "--max-size="):
			maxSize, err := parseMaxSize(strings.TrimPrefix(arg, "--max-size="))
			if err != nil {
				return opts, err
			}
			opts.maxSize = maxSize

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

	if opts.upgrade && (opts.auto || len(opts.inputs) > 0 || len(opts.excludePatterns) > 0 || opts.include != nil || opts.exclude != nil || opts.ignoredDirs != nil || opts.fullPath || opts.maxSize > 0) {
		return opts, flagError("--upgrade cannot be combined with other options or inputs")
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

func parseMaxSize(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, flagError("--max-size must be greater than zero")
	}

	digitsEnd := 0
	for digitsEnd < len(value) && value[digitsEnd] >= '0' && value[digitsEnd] <= '9' {
		digitsEnd++
	}
	if digitsEnd == 0 {
		return 0, flagError(fmt.Sprintf("invalid --max-size value %q", value))
	}

	amount, err := strconv.ParseUint(value[:digitsEnd], 10, 64)
	if err != nil || amount == 0 {
		return 0, flagError(fmt.Sprintf("invalid --max-size value %q", value))
	}

	multipliers := map[string]uint64{
		"":    1,
		"B":   1,
		"KB":  1000,
		"MB":  1000 * 1000,
		"GB":  1000 * 1000 * 1000,
		"TB":  1000 * 1000 * 1000 * 1000,
		"KIB": 1 << 10,
		"MIB": 1 << 20,
		"GIB": 1 << 30,
		"TIB": 1 << 40,
	}
	multiplier, ok := multipliers[strings.ToUpper(value[digitsEnd:])]
	if !ok || amount > uint64((1<<63-1))/multiplier {
		return 0, flagError(fmt.Sprintf("invalid --max-size value %q", value))
	}

	return int64(amount * multiplier), nil
}

func exceedsMaxSize(size, maxSize int64) bool {
	return maxSize > 0 && size > maxSize
}

func usage() string {
	return `promptcat - concatenate text and source files for AI prompts

Usage:
  promptcat [options] <files...>
  promptcat auto [options]

Options:
  --help, -h            Show help
  --version, -v         Show version
  --upgrade             Download and install the latest release
  --max-size=1MB        Skip files larger than this size
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
		info, err := os.Lstat(root)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			for _, index := range indexes {
				fmt.Fprintf(os.Stderr, "Skipping (glob root not found): %s\n", globInputs[index].input)
			}
			continue
		}

		err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
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

type outputError struct {
	err error
}

func (e outputError) Error() string {
	return e.err.Error()
}

func (e outputError) Unwrap() error {
	return e.err
}

type outputWriter struct {
	writer io.Writer
}

func (w outputWriter) Write(data []byte) (int, error) {
	n, err := w.writer.Write(data)
	if err != nil {
		return n, outputError{err: err}
	}
	if n != len(data) {
		return n, outputError{err: io.ErrShortWrite}
	}
	return n, nil
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

func run(cliArgs []string, stdout, stderr io.Writer) error {
	opts, err := parseArgs(cliArgs)
	if err != nil {
		return fmt.Errorf("%s\n\n%s", err, usage())
	}
	if opts.upgrade {
		if err := upgrade(); err != nil {
			return fmt.Errorf("Upgrade failed: %w", err)
		}
		return nil
	}

	var args []string
	if opts.auto {
		args, err = selectAutoFiles(".", opts.ignoredDirs)
		if err != nil {
			return fmt.Errorf("Auto detection failed: %w", err)
		}
	} else {
		args, err = expandInputs(opts.inputs, opts.excludePatterns, opts.ignoredDirs)
		if err != nil {
			return fmt.Errorf("Failed to expand inputs: %w", err)
		}
	}

	if len(args) == 0 {
		return errors.New(usage())
	}

	output := bufio.NewWriter(outputWriter{writer: stdout})

	for _, input := range args {
		if isIgnored(input, opts.ignoredDirs) {
			fmt.Fprintf(stderr, "Skipping (ignored dir): %s\n", input)
			continue
		}

		info, err := os.Lstat(input)
		if err != nil {
			fmt.Fprintf(stderr, "Skipping (not found): %s\n", input)
			continue
		}

		if info.Mode()&os.ModeSymlink != 0 {
			fmt.Fprintf(stderr, "Skipping (symlink): %s\n", input)
			continue
		}

		if info.IsDir() {
			fmt.Fprintf(stderr, "Skipping (directory): %s\n", input)
			continue
		}

		if exceedsMaxSize(info.Size(), opts.maxSize) {
			fmt.Fprintf(stderr, "Skipping (too large): %s\n", input)
			continue
		}

		ext := strings.ToLower(filepath.Ext(input))

		if opts.include != nil && !opts.include[ext] {
			fmt.Fprintf(stderr, "Skipping (not included): %s\n", input)
			continue
		}

		if opts.exclude != nil && opts.exclude[ext] {
			fmt.Fprintf(stderr, "Skipping (excluded ext): %s\n", input)
			continue
		}

		if binaryExtensions[ext] {
			fmt.Fprintf(stderr, "Skipping (binary extension): %s\n", input)
			continue
		}

		file, err := os.Open(input)
		if err != nil {
			fmt.Fprintf(stderr, "Skipping (read error): %s\n", input)
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
				fmt.Fprintf(stderr, "Skipping (binary content): %s\n", input)
			} else if errors.As(err, new(outputError)) {
				return fmt.Errorf("writing output for %s: %w", input, err)
			} else {
				fmt.Fprintf(stderr, "Skipping (read error): %s\n", input)
			}
		}
	}

	if err := output.Flush(); err != nil {
		return fmt.Errorf("flushing output: %w", err)
	}
	return nil
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
