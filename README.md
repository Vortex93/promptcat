# promptcat

[![CI](https://github.com/Vortex93/promptcat/actions/workflows/ci.yml/badge.svg)](https://github.com/Vortex93/promptcat/actions/workflows/ci.yml)

`promptcat` turns a set of source or text files into one prompt-friendly document.

It is designed for the common workflow of collecting a focused slice of a repository and pasting it into ChatGPT, Claude, Copilot Chat, or another LLM without manually opening and copying files one by one.

```text
<<<FILE: cmd/promptcat/promptcat.go>>>
package main
...
<<<END FILE>>>
```

## Why promptcat

- Concatenate multiple files into one stable text stream
- Expand glob patterns inside the tool for predictable cross-shell behavior
- Filter inputs with `--include`, `--exclude`, and `--ignore-dir`
- Skip binary files automatically by extension and content detection
- Output relative paths by default or absolute paths with `--fullpath`

## Installation

`promptcat` currently installs from source with Go.

Requirements:
- Go `1.26` or newer

### Windows

PowerShell:

```powershell
go install github.com/Vortex93/promptcat/cmd/promptcat@latest
promptcat --version
```

If `promptcat` is not found, add `$(go env GOPATH)\bin` to your `PATH`.
The default location is usually `%USERPROFILE%\go\bin`.

Current terminal session:

```powershell
$env:Path += ";$(go env GOPATH)\bin"
```

### macOS

```bash
go install github.com/Vortex93/promptcat/cmd/promptcat@latest
promptcat --version
```

If `promptcat` is not found, add `$(go env GOPATH)/bin` to your `PATH`.
The default location is usually `~/go/bin`.

Current terminal session:

```bash
export PATH="$(go env GOPATH)/bin:$PATH"
```

### Linux

```bash
go install github.com/Vortex93/promptcat/cmd/promptcat@latest
promptcat --version
```

If `promptcat` is not found, add `$(go env GOPATH)/bin` to your `PATH`.
The default location is usually `~/go/bin`.

Current terminal session:

```bash
export PATH="$(go env GOPATH)/bin:$PATH"
```

### Build From a Clone

```bash
git clone https://github.com/Vortex93/promptcat.git
cd promptcat
go test ./...
```

Build the binary locally:

macOS and Linux:

```bash
go build -o promptcat ./cmd/promptcat
```

Windows:

```powershell
go build -o promptcat.exe ./cmd/promptcat
```

## Quick Start

Collect all Go files under `cmd/`:

```bash
promptcat "cmd/**/*.go"
```

Collect only Go and Markdown files while ignoring noisy directories:

```bash
promptcat --include=go,md --ignore-dir=.git,node_modules "**/*"
```

Include absolute paths in the output:

```bash
promptcat --fullpath README.md "cmd/**/*.go"
```

Write the result to a file you can paste elsewhere:

```bash
promptcat --include=go,md README.md "cmd/**/*.go" > prompt.txt
```

## Usage

```text
promptcat [options] <files...>
```

Inputs can be a mix of direct file paths and glob patterns.
Directories passed directly are skipped.

### Options

| Option | Description |
| --- | --- |
| `-h`, `--help` | Show help output |
| `-v`, `--version` | Show version and build metadata |
| `--fullpath` | Output absolute paths instead of the provided relative paths |
| `--include=go,md` | Include only these extensions |
| `--exclude=json,lock` | Exclude these extensions |
| `--ignore-dir=.git,node_modules` | Skip files whose path contains any of these directory names |

Notes:
- Extensions can be written with or without a leading dot
- `--include`, `--exclude`, and `--ignore-dir` accept comma-separated values
- Binary files are skipped automatically

## Globs and Shells

Quote glob patterns so `promptcat` expands them itself instead of letting your shell do it first.
That keeps behavior more consistent across Bash, Zsh, Fish, PowerShell, and `cmd.exe`.

Recommended forms:

- macOS and Linux shells: `promptcat "cmd/**/*.go"`
- PowerShell: `promptcat 'cmd/**/*.go'`
- Windows Command Prompt: `promptcat "cmd/**/*.go"`

Prefer forward slashes in patterns even on Windows.
Examples such as `cmd/**/*.go` work across platforms.

Supported pattern features:

- `*` matches within a single path segment
- `?` matches a single character within a path segment
- `**` matches across directories

## Output Format

Each file is wrapped in markers:

```text
<<<FILE: path/to/file>>>
<file contents>
<<<END FILE>>>
```

This makes the output easy to paste into prompts and easy for downstream tooling to split or parse again later.

## Development

The repository includes a small `Taskfile` for local workflows.

```bash
task build
task test
task install
```

Direct Go commands work as well:

```bash
go build ./cmd/promptcat
go test ./...
go install ./cmd/promptcat
```

Continuous integration runs the build and test workflow on Windows, macOS, and Linux.

## Contributing

Guidelines live in [`CONTRIBUTING.md`](./CONTRIBUTING.md).

## License

This project is licensed under the [MIT License](./LICENSE).
