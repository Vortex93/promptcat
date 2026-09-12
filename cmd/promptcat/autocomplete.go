package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

var supportedAutocompleteShells = []string{"bash", "fish", "zsh", "powershell"}

func runAutocomplete(opts options, stdout, stderr io.Writer) error {
	shell := strings.ToLower(strings.TrimSpace(opts.autocompleteShell))
	if shell == "" {
		shell = detectAutocompleteShell()
	}
	script, err := autocompleteScript(shell)
	if err != nil {
		return err
	}
	if !opts.autocompleteInstall {
		_, err := io.WriteString(stdout, script)
		return err
	}

	path, err := autocompleteInstallPath(shell)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create autocomplete directory: %w", err)
	}
	content := []byte(script)
	if strings.ToLower(shell) == "powershell" || strings.ToLower(shell) == "pwsh" || strings.ToLower(shell) == "ps" {
		const begin = "# BEGIN promptcat autocomplete"
		const end = "# END promptcat autocomplete"
		existing, readErr := os.ReadFile(path)
		if readErr != nil && !os.IsNotExist(readErr) {
			return fmt.Errorf("read PowerShell profile: %w", readErr)
		}
		block := begin + "\n" + script + end + "\n"
		text := string(existing)
		if start := strings.Index(text, begin); start >= 0 {
			if finish := strings.Index(text[start:], end); finish >= 0 {
				finish += start + len(end)
				text = text[:start] + block + text[finish:]
			} else {
				text += "\n" + block
			}
		} else {
			if text != "" && !strings.HasSuffix(text, "\n") {
				text += "\n"
			}
			text += block
		}
		content = []byte(text)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return fmt.Errorf("install %s autocomplete: %w", shell, err)
	}
	fmt.Fprintf(stderr, "Installed %s autocomplete at %s\n", shell, path)
	return nil
}

func detectAutocompleteShell() string {
	if runtime.GOOS == "windows" {
		return "powershell"
	}
	shell := filepath.Base(os.Getenv("SHELL"))
	if shell == "" {
		return "bash"
	}
	return shell
}

func autocompleteScript(shell string) (string, error) {
	switch strings.ToLower(shell) {
	case "bash":
		return bashAutocomplete, nil
	case "fish":
		return fishAutocomplete, nil
	case "zsh":
		return zshAutocomplete, nil
	case "powershell", "pwsh", "ps":
		return powershellAutocomplete, nil
	default:
		return "", fmt.Errorf("unsupported shell %q (supported: %s)", shell, strings.Join(supportedAutocompleteShells, ", "))
	}
}

func autocompleteInstallPath(shell string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	switch strings.ToLower(shell) {
	case "bash":
		return filepath.Join(home, ".local", "share", "bash-completion", "completions", "promptcat"), nil
	case "fish":
		return filepath.Join(home, ".config", "fish", "completions", "promptcat.fish"), nil
	case "zsh":
		return filepath.Join(home, ".local", "share", "zsh", "site-functions", "_promptcat"), nil
	case "powershell", "pwsh", "ps":
		profile := os.Getenv("PROFILE")
		if profile == "" {
			profile = filepath.Join(home, ".config", "powershell", "Microsoft.PowerShell_profile.ps1")
		}
		return profile, nil
	default:
		return "", fmt.Errorf("unsupported shell %q (supported: %s)", shell, strings.Join(supportedAutocompleteShells, ", "))
	}
}

const bashAutocomplete = `# promptcat bash completion
_promptcat() {
    local cur=${COMP_WORDS[COMP_CWORD]}
    local commands="auto archive autocomplete"
    local options="--help --version --upgrade --max-size --fullpath --include --pattern --output --exclude --ignore-dir"
    if [[ ${COMP_CWORD} -eq 1 ]]; then
        COMPREPLY=($(compgen -W "${commands} ${options}" -- "${cur}"))
    elif [[ ${COMP_WORDS[1]} == autocomplete && ${COMP_CWORD} -eq 2 ]]; then
        COMPREPLY=($(compgen -W "install bash fish zsh powershell" -- "${cur}"))
    elif [[ ${COMP_WORDS[1]} == archive && ${COMP_CWORD} -eq 2 ]]; then
        COMPREPLY=($(compgen -W "${options}" -- "${cur}"))
    elif [[ ${cur} == -* ]]; then
        COMPREPLY=($(compgen -W "${options}" -- "${cur}"))
    else
        COMPREPLY=($(compgen -f -- "${cur}"))
    fi
}
complete -F _promptcat promptcat
`

const fishAutocomplete = `# promptcat fish completion
complete -c promptcat -f
complete -c promptcat -n '__fish_use_subcommand' -a 'auto archive autocomplete' -d 'Promptcat command'
complete -c promptcat -n '__fish_seen_subcommand_from autocomplete' -a 'install bash fish zsh powershell' -d 'Completion target'
complete -c promptcat -n '__fish_use_subcommand' -l help -s h -d 'Show help'
complete -c promptcat -n '__fish_use_subcommand' -l version -s v -d 'Show version'
complete -c promptcat -n '__fish_use_subcommand' -l upgrade -d 'Upgrade promptcat'
complete -c promptcat -n '__fish_seen_subcommand_from archive' -l include -r -d 'Add archive patterns'
complete -c promptcat -n '__fish_seen_subcommand_from archive' -l pattern -r -d 'Replace archive patterns'
complete -c promptcat -n '__fish_seen_subcommand_from archive' -l output -r -d 'Archive output path'
complete -c promptcat -n '__fish_seen_subcommand_from archive' -l max-size -r -d 'Maximum file size'
complete -c promptcat -n '__fish_seen_subcommand_from archive' -l ignore-dir -r -d 'Ignored directory'
complete -c promptcat -n '__fish_seen_subcommand_from archive' -a '(__fish_complete_path)'
`

const zshAutocomplete = `#compdef promptcat

_promptcat() {
    _arguments '1:command:(auto archive autocomplete)' \\
        '*:file:_files'
}

_promptcat "$@"
`

const powershellAutocomplete = `Register-ArgumentCompleter -Native -CommandName promptcat -ScriptBlock {
    param($wordToComplete, $commandAst, $cursorPosition)
    $commands = @('auto', 'archive', 'autocomplete')
    $options = @('--help', '--version', '--upgrade', '--max-size', '--fullpath', '--include', '--pattern', '--output', '--exclude', '--ignore-dir')
    $tokens = $commandAst.CommandElements | ForEach-Object { $_.ToString() }
    $candidates = if ($tokens.Count -le 1) { $commands + $options } else { $options }
    $candidates | Where-Object { $_ -like "$wordToComplete*" } | ForEach-Object {
        [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_)
    }
}
`
