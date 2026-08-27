package main

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

func walkDirUnsorted(root string, walkFn func(string, fs.DirEntry) error) error {
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}

	return walkDirUnsortedEntry(root, fs.FileInfoToDirEntry(info), walkFn)
}

func walkDirUnsortedEntry(path string, entry fs.DirEntry, walkFn func(string, fs.DirEntry) error) error {
	if err := walkFn(path, entry); err != nil {
		if err == filepath.SkipDir {
			return nil
		}
		return err
	}
	if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
		return nil
	}

	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	var entries []fs.DirEntry
	for {
		batch, readErr := directory.ReadDir(256)
		entries = append(entries, batch...)
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			directory.Close()
			return readErr
		}
	}
	closeErr := directory.Close()
	if closeErr != nil {
		return closeErr
	}

	for _, child := range entries {
		if err := walkDirUnsortedEntry(filepath.Join(path, child.Name()), child, walkFn); err != nil {
			return err
		}
	}
	return nil
}
