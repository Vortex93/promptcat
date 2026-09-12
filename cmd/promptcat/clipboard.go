package main

import (
	"bytes"
	"fmt"
	"os/exec"
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
