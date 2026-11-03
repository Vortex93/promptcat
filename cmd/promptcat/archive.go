package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const defaultArchiveMaxSize = 1 << 20

var defaultArchiveIgnoredDirs = []string{
	".git", ".svn", ".hg", ".promptcat", "node_modules", "vendor", ".venv", "venv", "env", "__pycache__",
	"dist", "build", "out", "target", "coverage", ".storybook", "storybook-static",
	".next", ".nuxt", ".svelte-kit", ".astro", ".cache", ".turbo", ".pytest_cache",
	".mypy_cache", ".ruff_cache", ".tox", ".nox", ".pixi", ".gradle", ".idea", "tmp", "temp", "logs",
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
		opts.archivePatterns = []string{"**"}
	}
}

// resolveArchiveOutput resolves paths relative to the archive root. ZIP output
// directories must already exist and use the project name; explicit filenames
// retain their spelling and ZIP replacement remains atomic in writeZIP.
func resolveArchiveOutput(root, output string, zipOutput bool) (string, error) {
	if output == "" {
		output = "archive.tar.zst"
		if zipOutput {
			output = "archive.zip"
		}
	}
	if output == "~" || strings.HasPrefix(output, "~/") || strings.HasPrefix(output, "~\\") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve archive output home directory: %w", err)
		}
		output = filepath.Join(home, strings.TrimLeft(output[1:], "/\\"))
	}
	if !filepath.IsAbs(output) {
		output = filepath.Join(root, output)
	}
	output = filepath.Clean(output)
	if !zipOutput {
		return output, nil
	}
	info, err := os.Stat(output)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect archive output: %w", err)
	}
	if err == nil && info.IsDir() {
		projectName := filepath.Base(root)
		if projectName == "." || projectName == string(filepath.Separator) || projectName == filepath.VolumeName(root) || projectName == filepath.VolumeName(root)+string(filepath.Separator) {
			return "", errors.New("cannot derive an archive name from a filesystem root; provide a ZIP filename")
		}
		output = filepath.Join(output, projectName+".zip")
	}
	if !strings.EqualFold(filepath.Ext(output), ".zip") {
		return "", flagError("archive --chatgpt output must be a .zip file or existing directory")
	}
	destination, err := os.Lstat(output)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect ZIP destination: %w", err)
	}
	if err == nil && !destination.Mode().IsRegular() {
		return "", errors.New("ZIP output must be a regular file or an existing directory")
	}
	parent, err := os.Stat(filepath.Dir(output))
	if err != nil {
		return "", fmt.Errorf("inspect archive output directory: %w", err)
	}
	if !parent.IsDir() {
		return "", errors.New("archive output parent is not a directory")
	}
	return output, nil
}

func runArchive(opts options, stdout, stderr io.Writer) error {
	var progress *packProgress
	if opts.archiveChatGPT {
		progress = newPackProgress(stdout, stderr, opts.archiveExplain || opts.archiveVerbose)
		defer progress.fail()
	}
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
	outputPath, err := resolveArchiveOutput(root, opts.archiveOutput, opts.archiveChatGPT)
	if err != nil {
		return err
	}

	files, skipped, err := selectArchiveFiles(root, outputPath, opts, stderr)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return errors.New("archive found no matching files")
	}

	if opts.archiveChatGPT {
		return runChatGPTArchive(opts, root, outputPath, files, skipped, stderr, progress)
	}
	if opts.archiveVerbose {
		for _, file := range files {
			fmt.Fprintf(stderr, "Included: %s\n", filepath.ToSlash(file))
		}
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
