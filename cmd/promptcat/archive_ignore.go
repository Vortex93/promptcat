package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	gitignore "github.com/denormal/go-gitignore"
)

// standaloneGitIgnored applies nested .gitignore rules in directory order.
// Parent directories must remain included before a child can be re-included,
// matching Git's rule that an ignored directory cannot be re-entered.
func standaloneGitIgnored(root string, paths []string) (map[string]bool, error) {
	ignored := make(map[string]bool)
	matchers := make(map[string]gitignore.GitIgnore)
	loaded := make(map[string]bool)
	for _, path := range paths {
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return nil, fmt.Errorf("resolve standalone ignore path %s: %w", path, err)
		}
		if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			return nil, fmt.Errorf("standalone ignore path outside archive root: %s", path)
		}
		parts := strings.Split(relative, string(filepath.Separator))
		current := root
		var active []gitignore.GitIgnore
		for index, part := range parts {
			// A nested file overrides parent rules only if its directory is visible.
			if !loaded[current] {
				loaded[current] = true
				ignorePath := filepath.Join(current, ".gitignore")
				info, statErr := os.Lstat(ignorePath)
				if statErr != nil && !os.IsNotExist(statErr) {
					return nil, fmt.Errorf("inspect %s: %w", ignorePath, statErr)
				}
				if statErr == nil && info.Mode().IsRegular() {
					input, openErr := os.Open(ignorePath)
					if openErr != nil {
						return nil, fmt.Errorf("open %s: %w", ignorePath, openErr)
					}
					var parseErr error
					matcher := gitignore.New(input, current, func(err gitignore.Error) bool {
						parseErr = fmt.Errorf("parse %s: %w", ignorePath, err.Underlying())
						return false
					})
					closeErr := input.Close()
					if parseErr != nil {
						return nil, parseErr
					}
					if closeErr != nil {
						return nil, fmt.Errorf("close %s: %w", ignorePath, closeErr)
					}
					matchers[current] = matcher
				}
			}
			if matcher := matchers[current]; matcher != nil {
				active = append(active, matcher)
			}
			current = filepath.Join(current, part)
			isDir := index < len(parts)-1
			matchedIgnore := false
			for _, matcher := range active {
				candidate, relErr := filepath.Rel(matcher.Base(), current)
				if relErr != nil {
					return nil, relErr
				}
				if match := matcher.Relative(candidate, isDir); match != nil {
					matchedIgnore = match.Ignore()
				}
			}
			if matchedIgnore {
				ignored[path] = true
				break
			}
		}
	}
	return ignored, nil
}
