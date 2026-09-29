package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const defaultArchiveMaxSize = 1 << 20

var defaultArchiveIgnoredDirs = []string{
	".git", ".svn", ".hg", ".promptcat", "node_modules", "vendor", ".venv", "venv", "env", "__pycache__",
	"dist", "build", "out", "target", "coverage", ".storybook", "storybook-static",
	".next", ".nuxt", ".svelte-kit", ".astro", ".cache", ".turbo", ".pytest_cache",
	".mypy_cache", ".ruff_cache", ".tox", ".nox", ".pixi", ".gradle", ".idea", "tmp", "temp", "logs",
}

var aiArchiveContextPatterns = []string{
	"**.md",
	"**/go.mod", "**/go.sum", "**/go.work", "**/go.work.sum",
	"**/package.json", "**/package-lock.json", "**/pnpm-lock.yaml", "**/yarn.lock", "**/bun.lock", "**/bun.lockb",
	"**/Cargo.toml", "**/Cargo.lock",
	"**/pyproject.toml", "**/requirements.txt", "**/requirements-dev.txt", "**/Pipfile", "**/Pipfile.lock", "**/poetry.lock",
	"**/Dockerfile", "**/Dockerfile.*", "**/docker-compose.yml", "**/docker-compose.yaml", "**/compose.yml", "**/compose.yaml",
	"**/Makefile", "**/Taskfile.yml", "**/Taskfile.yaml",
	"**/.env.example", "**/.env.sample",
}

var defaultArchivePatterns = []string{
	"**.js", "**.ts", "**.py", "**.go", "**.rs", "**.css", "**.scss",
	"**.mjs", "**.cjs", "**.jsx", "**.tsx", "**.html", "**.vue", "**.svelte",
	"**.c", "**.h", "**.cc", "**.cpp", "**.hpp", "**.java", "**.kt", "**.kts",
	"**.dart", "**.cs", "**.php", "**.rb", "**.swift", "**.ex", "**.exs",
	"**.json", "**.yaml", "**.yml", "**.toml", "**.xml", "**.ini", "**.sql",
	"**.proto", "**.graphql", "**.tf", "**.sh", "**.bash", "**.zsh",
	"**.lua", "**.gd", "**.tscn", "**.scala", "**.groovy", "**.zig", "**.fs", "**.fsx",
	"**.clj", "**.cljs", "**.cljc", "**.pl", "**.pm", "**.r",
}

func applyArchiveDefaults(opts *options) {
	if opts.maxSize == 0 {
		opts.maxSize = defaultArchiveMaxSize
	}
	archiveIgnoredDirs := parseDirs(strings.Join(defaultArchiveIgnoredDirs, ","))
	for name := range opts.ignoredDirs {
		archiveIgnoredDirs[name] = true
	}
	if opts.archiveAI {
		archiveIgnoredDirs[".agentpack"] = true
	}
	opts.ignoredDirs = archiveIgnoredDirs
	if len(opts.archivePatterns) == 0 {
		opts.archivePatterns = append([]string(nil), defaultArchivePatterns...)
		if opts.archiveAI {
			opts.archivePatterns = append(opts.archivePatterns, aiArchiveContextPatterns...)
		}
	}
}

func runArchive(opts options, stderr io.Writer) error {
	root := "."
	if len(opts.inputs) == 1 {
		root = opts.inputs[0]
	}
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("archive folder: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("archive input is not a folder: %s", root)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve archive folder: %w", err)
	}
	outputPath := opts.archiveOutput
	if !filepath.IsAbs(outputPath) {
		outputPath = filepath.Join(root, outputPath)
	}
	outputPath = filepath.Clean(outputPath)

	inputs := make([]string, 0, len(opts.archivePatterns))
	for _, pattern := range opts.archivePatterns {
		inputs = append(inputs, filepath.Join(root, filepath.FromSlash(pattern)))
	}
	paths, err := expandInputs(inputs, nil, opts.ignoredDirs)
	if err != nil {
		return fmt.Errorf("expand archive patterns: %w", err)
	}

	files := make([]string, 0, len(paths))
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		info, statErr := os.Lstat(path)
		if statErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			continue
		}
		if exceedsMaxSize(info.Size(), opts.maxSize) {
			fmt.Fprintf(stderr, "Skipping (too large): %s\n", path)
			continue
		}
		if filepath.Clean(path) == outputPath {
			continue
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		relative = filepath.Clean(relative)
		if !seen[relative] {
			seen[relative] = true
			files = append(files, relative)
		}
	}
	sort.Strings(files)
	if len(files) == 0 {
		return errors.New("archive found no matching files")
	}

	if opts.archiveFiles && !opts.archiveAI {
		if err := writeTarZst(outputPath, root, files); err != nil {
			return err
		}
		fmt.Fprintf(stderr, "Archived %d files to %s\n", len(files), outputPath)
		return nil
	}

	temporaryDirectory, err := os.MkdirTemp("", "promptcat-archive-")
	if err != nil {
		return fmt.Errorf("create temporary archive directory: %w", err)
	}
	defer os.RemoveAll(temporaryDirectory)

	if opts.archiveFiles {
		archiveFiles := make([]string, 0, len(files)+8)
		for _, file := range files {
			if err := stageArchiveFile(root, temporaryDirectory, file); err != nil {
				return err
			}
			archiveFiles = append(archiveFiles, filepath.ToSlash(file))
		}
		packFiles, err := writeAIArchivePack(root, files, temporaryDirectory)
		if err != nil {
			return err
		}
		archiveFiles = append(archiveFiles, packFiles...)
		if err := writeTarZst(outputPath, temporaryDirectory, archiveFiles); err != nil {
			return err
		}
		fmt.Fprintf(stderr, "Archived %d files with AI navigation pack to %s\n", len(files), outputPath)
		return nil
	}

	exportPath := filepath.Join(temporaryDirectory, "export.txt")
	exportFile, err := os.Create(exportPath)
	if err != nil {
		return fmt.Errorf("create archive export: %w", err)
	}
	output := bufio.NewWriterSize(exportFile, 256*1024)
	tasks := make([]fileTask, 0, len(files))
	for _, file := range files {
		tasks = append(tasks, fileTask{input: filepath.Join(root, filepath.FromSlash(file)), path: filepath.ToSlash(file)})
	}
	streamErr := streamFiles(output, tasks, stderr)
	flushErr := output.Flush()
	closeErr := exportFile.Close()
	if streamErr != nil {
		return fmt.Errorf("create archive export: %w", streamErr)
	}
	if flushErr != nil {
		return fmt.Errorf("flush archive export: %w", flushErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close archive export: %w", closeErr)
	}
	exportInfo, err := os.Stat(exportPath)
	if err != nil {
		return fmt.Errorf("stat archive export: %w", err)
	}
	if exportInfo.Size() == 0 {
		return errors.New("archive found no readable text files")
	}
	archiveFiles := []string{"export.txt"}
	if opts.archiveAI {
		packFiles, packErr := writeAIArchivePack(root, files, temporaryDirectory)
		if packErr != nil {
			return packErr
		}
		archiveFiles = append(archiveFiles, packFiles...)
	}
	if err := writeTarZst(outputPath, temporaryDirectory, archiveFiles); err != nil {
		return err
	}
	if opts.archiveClipboard {
		if err := copyFileToClipboard(outputPath); err != nil {
			return err
		}
		fmt.Fprintln(stderr, "Copied archive file to the clipboard")
	}
	if opts.archiveAI {
		fmt.Fprintf(stderr, "Archived %d files as export.txt with AI navigation pack to %s\n", len(files), outputPath)
	} else {
		fmt.Fprintf(stderr, "Archived %d files as export.txt to %s\n", len(files), outputPath)
	}
	return nil
}

func stageArchiveFile(root, destination, relative string) error {
	source := filepath.Join(root, filepath.FromSlash(relative))
	target := filepath.Join(destination, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create archive staging directory: %w", err)
	}
	if err := os.Link(source, target); err == nil {
		return nil
	}

	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open archive source %s: %w", relative, err)
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return fmt.Errorf("stat archive source %s: %w", relative, err)
	}
	output, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("create staged archive file %s: %w", relative, err)
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return fmt.Errorf("copy archive source %s: %w", relative, err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("close staged archive file %s: %w", relative, err)
	}
	return nil
}

func writeTarZst(outputPath, root string, files []string) error {
	tarCommand := exec.Command("tar", "-cf", "-", "--null", "--files-from=-")
	tarCommand.Dir = root
	tarOutput, err := tarCommand.StdoutPipe()
	if err != nil {
		return fmt.Errorf("start tar: %w", err)
	}
	tarInput, err := tarCommand.StdinPipe()
	if err != nil {
		return fmt.Errorf("open tar file list: %w", err)
	}
	tarCommand.Stderr = os.Stderr

	zstdCommand := exec.Command("zstd", "-3", "--quiet", "-f", "-o", outputPath)
	zstdCommand.Dir = root
	zstdCommand.Stdin = tarOutput
	zstdCommand.Stderr = os.Stderr
	if err := tarCommand.Start(); err != nil {
		return fmt.Errorf("start tar: %w", err)
	}
	if err := zstdCommand.Start(); err != nil {
		_ = tarCommand.Process.Kill()
		_ = tarCommand.Wait()
		return fmt.Errorf("start zstd: %w", err)
	}

	writer := bufio.NewWriter(tarInput)
	for _, file := range files {
		if _, err := writer.WriteString(filepath.ToSlash(file)); err != nil {
			_ = tarInput.Close()
			return fmt.Errorf("write tar file list: %w", err)
		}
		if err := writer.WriteByte(0); err != nil {
			_ = tarInput.Close()
			return fmt.Errorf("write tar file list: %w", err)
		}
	}
	if err := writer.Flush(); err != nil {
		_ = tarInput.Close()
		return fmt.Errorf("write tar file list: %w", err)
	}
	if err := tarInput.Close(); err != nil {
		return fmt.Errorf("close tar file list: %w", err)
	}

	tarErr := tarCommand.Wait()
	zstdErr := zstdCommand.Wait()
	if tarErr != nil {
		_ = os.Remove(outputPath)
		return fmt.Errorf("tar archive: %w", tarErr)
	}
	if zstdErr != nil {
		_ = os.Remove(outputPath)
		return fmt.Errorf("zstd archive: %w", zstdErr)
	}
	return nil
}
