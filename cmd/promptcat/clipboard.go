package main

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

type clipboardBackend struct {
	name string
	args []string
}

// Directory inputs use the same selection and limits as bare `clipboard`.
// Explicit files/globs keep their existing behavior; source symlinks stay skipped.
func expandClipboardInputs(opts *options, stderr io.Writer) ([]string, error) {
	inputs := opts.inputs
	if len(inputs) == 0 {
		inputs = []string{"."}
	}

	// Find directories before expanding so defaults apply to the whole selection.
	directories := make(map[string]bool)
	for _, input := range inputs {
		if hasGlob(input) {
			continue
		}
		info, err := os.Lstat(input)
		if err == nil && info.IsDir() {
			directories[input] = true
		}
	}
	if len(directories) > 0 {
		applyArchiveDefaults(opts)
	}

	var patterns []string
	var selected []string
	for _, input := range inputs {
		if directories[input] {
			root, err := filepath.Abs(input)
			if err != nil {
				return nil, err
			}
			files, _, err := selectArchiveFiles(root, "", *opts, stderr)
			if err != nil {
				return nil, err
			}
			for _, file := range files {
				selected = append(selected, filepath.Join(input, file))
			}
		} else {
			patterns = append(patterns, input)
		}
	}
	paths, err := expandInputs(patterns, opts.excludePatterns, opts.ignoredDirs)
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		ignored := isIgnored(path, opts.ignoredDirs)
		// Ignore names within selected folders, not ancestors such as /tmp.
		for directory := range directories {
			relative, err := filepath.Rel(directory, path)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				continue
			}
			if !isIgnored(relative, opts.ignoredDirs) {
				ignored = false
				break
			}
		}
		if !ignored {
			selected = append(selected, path)
		}
	}
	matchers := make([]*regexp.Regexp, 0, len(opts.excludePatterns))
	for _, pattern := range opts.excludePatterns {
		matcher, err := globToRegex(pattern)
		if err != nil {
			return nil, err
		}
		matchers = append(matchers, matcher)
	}
	seen := make(map[string]bool)
	var result []string
	for _, path := range selected {
		if seen[path] || matchesAnyPattern(trimDotSlash(filepath.ToSlash(path)), matchers) {
			continue
		}
		seen[path] = true
		result = append(result, path)
	}
	return result, nil
}

func clipboardBackends() []clipboardBackend {
	switch runtime.GOOS {
	case "darwin":
		return []clipboardBackend{{name: "pbcopy"}}
	case "windows":
		return []clipboardBackend{{name: "clip"}}
	default:
		return []clipboardBackend{
			{name: "wl-copy"},
			{name: "xclip", args: []string{"-selection", "clipboard"}},
			{name: "xsel", args: []string{"--clipboard", "--input"}},
			{name: "pbcopy"},
		}
	}
}

func copyToClipboard(data []byte) error {
	var attempted []string
	for _, backend := range clipboardBackends() {
		if _, err := exec.LookPath(backend.name); err != nil {
			continue
		}
		attempted = append(attempted, backend.name)
		command := exec.Command(backend.name, backend.args...)
		command.Stdin = bytes.NewReader(data)
		if err := command.Run(); err == nil {
			return nil
		}
	}
	if len(attempted) == 0 {
		return fmt.Errorf("no supported clipboard command found (tried wl-copy, xclip, xsel, pbcopy, and clip)")
	}
	return fmt.Errorf("clipboard command failed (tried %v)", attempted)
}

func copyFileToClipboard(path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve clipboard file: %w", err)
	}
	uri := (&url.URL{Scheme: "file", Path: absPath}).String() + "\n"

	backends := []clipboardBackend{}
	switch runtime.GOOS {
	case "darwin":
		backends = []clipboardBackend{{name: "osascript", args: []string{"-e", fmt.Sprintf("set the clipboard to POSIX file %q", absPath)}}}
	case "windows":
		backends = []clipboardBackend{
			{name: "powershell", args: []string{"-NoProfile", "-Command", "Set-Clipboard -LiteralPath $args[0]", "--", absPath}},
			{name: "pwsh", args: []string{"-NoProfile", "-Command", "Set-Clipboard -LiteralPath $args[0]", "--", absPath}},
		}
	default:
		backends = []clipboardBackend{
			{name: "wl-copy", args: []string{"--type", "text/uri-list"}},
			{name: "xclip", args: []string{"-selection", "clipboard", "-t", "text/uri-list"}},
			{name: "xsel", args: []string{"--clipboard", "--input"}},
		}
	}

	var attempted []string
	for _, backend := range backends {
		if _, err := exec.LookPath(backend.name); err != nil {
			continue
		}
		attempted = append(attempted, backend.name)
		command := exec.Command(backend.name, backend.args...)
		if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
			command.Stdin = bytes.NewReader([]byte(uri))
		}
		if err := command.Run(); err == nil {
			return nil
		}
	}
	if len(attempted) == 0 {
		return fmt.Errorf("no supported file clipboard command found")
	}
	return fmt.Errorf("file clipboard command failed (tried %v)", attempted)
}
