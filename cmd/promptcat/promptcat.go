package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var version = "1.0.0"
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
			set[d] = true
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
		if ignored[p] {
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
	fullPath    bool
	include     map[string]bool
	exclude     map[string]bool
	ignoredDirs map[string]bool
	inputs      []string
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
			i++
			if i >= len(args) {
				return opts, flagError("missing value for --ignore-dir")
			}
			opts.ignoredDirs = parseDirs(args[i])

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

		default:
			opts.inputs = append(opts.inputs, arg)
		}
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

Options:
  --help, -h            Show help
  --version, -v         Show version
  --fullpath            Output absolute file paths
  --include=go,md       Include only specific extensions
  --exclude=json        Exclude extensions
  --ignore-dir=name     Ignore directories by name

Output format:
  <<<FILE: path/to/file>>>
  <file contents>
  <<<END FILE>>>

Examples:
  promptcat "cmd/**/*.go"
  promptcat --include=go,md --ignore-dir=.git,node_modules "**/*"
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
		case '.', '(', ')', '+', '|', '^', '$', '{', '}', '[', ']', '\\':
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
	parts := strings.Split(filepath.ToSlash(pattern), "/")
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
		return "."
	}

	return filepath.FromSlash(strings.Join(rootParts, "/"))
}

func expandInput(input string) []string {
	if !hasGlob(input) {
		return []string{input}
	}

	matcher, err := globToRegex(input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Skipping (bad pattern): %s\n", input)
		return nil
	}

	root := globRoot(input)
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "Skipping (glob root not found): %s\n", input)
		return nil
	}

	matches := make([]string, 0)
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}

		if d.IsDir() {
			return nil
		}

		if matcher.MatchString(trimDotSlash(path)) {
			matches = append(matches, path)
		}

		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Skipping (walk error): %s\n", input)
		return nil
	}

	sort.Strings(matches)
	return matches
}

func expandInputs(inputs []string) []string {
	expanded := make([]string, 0, len(inputs))
	seen := map[string]bool{}

	for _, input := range inputs {
		for _, match := range expandInput(input) {
			if seen[match] {
				continue
			}

			seen[match] = true
			expanded = append(expanded, match)
		}
	}

	return expanded
}

func writeFileBlock(output *bytes.Buffer, path string, data []byte) {
	output.WriteString(fileStartMarkerPrefix)
	output.WriteString(filepath.ToSlash(path))
	output.WriteString(fileStartMarkerSuffix)
	output.WriteString("\n")

	output.Write(bytes.TrimRight(data, "\n"))
	output.WriteString("\n")

	output.WriteString(fileEndMarker)
	output.WriteString("\n\n")
}

func main() {
	opts, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, usage())
		os.Exit(1)
	}

	args := expandInputs(opts.inputs)

	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, usage())
		os.Exit(1)
	}

	var output bytes.Buffer

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

		data, err := os.ReadFile(input)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Skipping (read error): %s\n", input)
			continue
		}

		if !isProbablyText(data) {
			fmt.Fprintf(os.Stderr, "Skipping (binary content): %s\n", input)
			continue
		}

		path := input
		if opts.fullPath {
			abs, err := filepath.Abs(input)
			if err == nil {
				path = abs
			}
		}

		writeFileBlock(&output, path, data)
	}

	fmt.Print(output.String())
}
