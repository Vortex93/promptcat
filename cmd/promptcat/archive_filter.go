package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

var archivePrivateKeyPattern = regexp.MustCompile(`-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY-----`)

// selectArchiveFiles applies the same ordered rejection rules to every archive
// format. A rejected path has one reason; explicit patterns never bypass safety.
// Git decides ignore semantics, while content checks are conservative heuristics
// and cannot guarantee that accepted text contains no secrets.
func selectArchiveFiles(root, outputPath string, opts options, stderr io.Writer) ([]string, []chatGPTSkippedFile, error) {
	matchers := make([]*regexp.Regexp, 0, len(opts.archivePatterns))
	for _, pattern := range opts.archivePatterns {
		matcher, err := globToRegex(filepath.ToSlash(filepath.Join(root, filepath.FromSlash(pattern))))
		if err != nil {
			return nil, nil, err
		}
		matchers = append(matchers, matcher)
	}
	excluded := make([]string, 0, len(opts.archiveExcludeRepos))
	for _, path := range opts.archiveExcludeRepos {
		excluded = append(excluded, filepath.Join(root, filepath.FromSlash(path)))
	}
	var candidates []string
	var skipped []chatGPTSkippedFile
	reject := func(path, reason string) {
		relative, err := filepath.Rel(root, path)
		if err != nil {
			relative = path
		}
		relative = filepath.ToSlash(relative)
		skipped = append(skipped, chatGPTSkippedFile{Path: relative, Reason: reason})
		if opts.archiveExplain || opts.archiveVerbose {
			fmt.Fprintf(stderr, "Skipping (%s): %s\n", reason, relative)
		}
	}

	// Prune excluded directories before reading files, and never follow links.
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if isWithinAnyPath(path, excluded) {
			reject(path, "excluded repository")
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() && (opts.ignoredDirs[strings.ToLower(entry.Name())] || strings.EqualFold(entry.Name(), ".git")) {
			reject(path, "ignored directory")
			return filepath.SkipDir
		}
		if entry.Type()&os.ModeSymlink != 0 {
			reject(path, "symlink")
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			reject(path, "non-regular file")
			return nil
		}
		if filepath.Clean(path) == outputPath {
			reject(path, "archive output")
			return nil
		}
		candidates = append(candidates, path)
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("walk archive: %w", err)
	}
	ignored, err := archiveGitIgnored(root, candidates)
	if err != nil {
		return nil, nil, err
	}
	files := make([]string, 0, len(candidates))
	for _, path := range candidates {
		if ignored[path] {
			reject(path, "gitignore")
			continue
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return nil, nil, err
		}
		reason, err := archiveFileReason(path, relative, opts.maxSize)
		if err != nil {
			return nil, nil, err
		}
		if reason != "" {
			reject(path, reason)
			continue
		}
		matched := false
		for _, matcher := range matchers {
			if matcher.MatchString(filepath.ToSlash(path)) {
				matched = true
				break
			}
		}
		if !matched {
			reject(path, "pattern mismatch")
			continue
		}
		files = append(files, relative)
	}
	sort.Strings(files)
	sort.Slice(skipped, func(i, j int) bool { return skipped[i].Path < skipped[j].Path })
	return files, skipped, nil
}

// archiveGitIgnored uses Git for repositories and the standalone matcher for
// paths outside one. Repository errors must not silently disable ignore rules.
func archiveGitIgnored(root string, candidates []string) (map[string]bool, error) {
	ignored := make(map[string]bool)
	repositories := make(map[string][]string)
	var standalone []string
	owners := make(map[string]string)
	for _, path := range candidates {
		dir := filepath.Dir(path)
		owner, ok := owners[dir]
		if !ok {
			current := dir
			var searched []string
			for {
				if cached, found := owners[current]; found {
					owner = cached
					break
				}
				searched = append(searched, current)
				if _, err := os.Lstat(filepath.Join(current, ".git")); err == nil {
					owner = current
					break
				} else if !os.IsNotExist(err) {
					return nil, fmt.Errorf("inspect repository %s: %w", current, err)
				}
				parent := filepath.Dir(current)
				if parent == current {
					break
				}
				current = parent
			}
			for _, visited := range searched {
				owners[visited] = owner
			}
		}
		if owner != "" {
			repositories[owner] = append(repositories[owner], path)
		} else {
			standalone = append(standalone, path)
		}
	}
	standaloneIgnored, err := standaloneGitIgnored(root, standalone)
	if err != nil {
		return nil, err
	}
	for path := range standaloneIgnored {
		ignored[path] = true
	}
	if len(repositories) == 0 {
		return ignored, nil
	}
	if _, err := exec.LookPath("git"); err != nil {
		return nil, fmt.Errorf("Git is required to apply ignore rules: %w", err)
	}
	for repository, paths := range repositories {
		var input bytes.Buffer
		for _, path := range paths {
			relative, err := filepath.Rel(repository, path)
			if err != nil {
				return nil, err
			}
			input.WriteString(filepath.ToSlash(relative))
			input.WriteByte(0)
		}
		command := exec.Command("git", "-C", repository, "check-ignore", "-z", "--stdin")
		command.Stdin = &input
		output, err := command.Output()
		if err != nil {
			if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
				return nil, fmt.Errorf("apply Git ignore rules in %s: %w", repository, err)
			}
		}
		for _, path := range bytes.Split(output, []byte{0}) {
			if len(path) != 0 {
				ignored[filepath.Join(repository, filepath.FromSlash(string(path)))] = true
			}
		}
	}
	return ignored, nil
}

var archiveMediaExtensions = map[string]bool{
	".svg": true, ".svgz": true, ".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".bmp": true, ".ico": true, ".tif": true, ".tiff": true, ".avif": true, ".heic": true, ".heif": true, ".psd": true, ".eps": true,
	".mp4": true, ".m4v": true, ".mov": true, ".avi": true, ".mkv": true, ".webm": true, ".mpeg": true, ".mpg": true, ".wmv": true, ".flv": true, ".mp3": true, ".wav": true, ".ogg": true, ".oga": true, ".opus": true, ".flac": true, ".m4a": true, ".aac": true, ".aiff": true, ".wma": true,
	".woff": true, ".woff2": true, ".ttf": true, ".otf": true, ".eot": true,
	".zip": true, ".gz": true, ".tgz": true, ".tar": true, ".zst": true, ".xz": true, ".bz2": true, ".7z": true, ".rar": true, ".jar": true, ".war": true, ".ear": true, ".pdf": true,
}

// archiveFileReason checks security before media, size, and readable content.
// Reading is bounded by the configured file limit and never logs file contents.
func archiveFileReason(path, relative string, maxSize int64) (string, error) {
	name := strings.ToLower(filepath.Base(relative))
	ext := strings.ToLower(filepath.Ext(name))
	example := strings.HasSuffix(name, ".example") || strings.HasSuffix(name, ".sample")
	sensitiveName := name == ".git" || name == ".netrc" || name == ".npmrc" || name == ".pypirc" || name == ".htpasswd" || name == ".pgpass"
	for _, component := range strings.Split(filepath.ToSlash(strings.ToLower(relative)), "/") {
		if component == ".ssh" || component == ".aws" || component == ".gnupg" || component == ".kube" {
			sensitiveName = true
		}
	}
	privateKeyName := ext == ".pem" || ext == ".key" || ext == ".p12" || ext == ".pfx" || ext == ".kdbx" || strings.HasPrefix(name, "id_rsa") || strings.HasPrefix(name, "id_ed25519") || strings.HasPrefix(name, "id_ecdsa")
	credentialName := strings.Contains(name, "credential") || strings.Contains(name, "secret") || strings.Contains(name, "password") || strings.Contains(name, "token")
	environmentName := name == ".env" || strings.HasPrefix(name, ".env.") || strings.HasSuffix(name, ".env") || strings.Contains(name, ".env.")
	if sensitiveName || privateKeyName || credentialName || (!example && environmentName) {
		return "sensitive path", nil
	}
	if archiveMediaExtensions[ext] || binaryExtensions[ext] {
		return "media or binary extension", nil
	}
	input, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open archive source %s: %w", relative, err)
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return "", fmt.Errorf("stat archive source %s: %w", relative, err)
	}
	if !info.Mode().IsRegular() {
		return "non-regular file", nil
	}
	if exceedsMaxSize(info.Size(), maxSize) {
		return "too large", nil
	}
	data, err := io.ReadAll(io.LimitReader(input, info.Size()+1))
	if err != nil {
		return "", fmt.Errorf("read archive source %s: %w", relative, err)
	}
	if int64(len(data)) != info.Size() {
		return "", fmt.Errorf("archive source changed size while reading: %s; retry the export", relative)
	}
	if archivePrivateKeyPattern.Match(data) {
		return "private key content", nil
	}
	contentType := http.DetectContentType(data)
	trimmed := bytes.TrimSpace(data)
	lowerText := bytes.ToLower(trimmed)
	if bytes.HasPrefix(lowerText, []byte("<svg")) || (bytes.HasPrefix(lowerText, []byte("<?xml")) && bytes.Contains(lowerText, []byte("<svg"))) {
		return "media or archive content", nil
	}
	mediaType := strings.HasPrefix(contentType, "image/") || strings.HasPrefix(contentType, "audio/") || strings.HasPrefix(contentType, "video/") || strings.HasPrefix(contentType, "font/")
	archiveType := strings.Contains(contentType, "zip") || strings.Contains(contentType, "gzip") || strings.Contains(contentType, "pdf")
	archiveSignature := bytes.HasPrefix(data, []byte("7z\xbc\xaf\x27\x1c")) || bytes.HasPrefix(data, []byte("Rar!\x1a\x07")) || bytes.HasPrefix(data, []byte("\x28\xb5\x2f\xfd")) || bytes.HasPrefix(data, []byte("\xfd7zXZ\x00")) || (len(data) >= 262 && string(data[257:262]) == "ustar")
	if mediaType || archiveType || archiveSignature {
		return "media or archive content", nil
	}
	if bytes.IndexByte(data, 0) >= 0 || !isProbablyText(data) {
		return "binary content", nil
	}
	if !utf8.Valid(data) {
		return "non-UTF-8 text", nil
	}
	return "", nil
}
