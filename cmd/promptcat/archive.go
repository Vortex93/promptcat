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

var defaultArchivePatterns = []string{
	"**.js", "**.ts", "**.py", "**.go", "**.rs", "**.css", "**.scss",
	"**.mjs", "**.cjs", "**.jsx", "**.tsx", "**.html", "**.vue", "**.svelte",
	"**.c", "**.h", "**.cc", "**.cpp", "**.hpp", "**.java", "**.kt", "**.kts",
	"**.dart", "**.cs", "**.php", "**.rb", "**.swift", "**.ex", "**.exs",
	"**.json", "**.yaml", "**.yml", "**.toml", "**.xml", "**.ini", "**.sql",
	"**.proto", "**.graphql", "**.tf", "**.sh", "**.bash", "**.zsh",
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

	if err := writeTarZst(outputPath, root, files); err != nil {
		return err
	}
	fmt.Fprintf(stderr, "Archived %d files to %s\n", len(files), outputPath)
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

	zstdCommand := exec.Command("zstd", "-3", "--quiet", "-o", outputPath)
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
