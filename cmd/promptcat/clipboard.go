package main

import (
	"bytes"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"runtime"
)

type clipboardBackend struct {
	name string
	args []string
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
