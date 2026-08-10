package main

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type autoStack struct {
	markers          map[string]bool
	sourceExtensions map[string]bool
	detectExtensions map[string]bool
	configNames      map[string]bool
}

type autoFile struct {
	path string
	name string
	ext  string
}

type autoStackIndex struct {
	activationByExt  map[string][]string
	activationByName map[string][]string
	matchingByExt    map[string][]string
	matchingByName   map[string][]string
}

var autoIgnoredDirs = map[string]bool{
	".astro": true, ".cache": true, ".git": true, ".next": true, ".nuxt": true,
	".svelte-kit": true, ".turbo": true, "bin": true, "build": true, "coverage": true,
	"dist": true, "node_modules": true, "out": true, "storybook-static": true, "target": true,
	"vendor": true,
}

var autoStacks = map[string]autoStack{
	"go": {
		markers:          autoSet("go.mod", "go.work"),
		sourceExtensions: autoSet(".go"),
		configNames:      autoSet("go.mod", "go.work"),
	},
	"javascript": {
		markers:          autoSet("package.json"),
		sourceExtensions: autoSet(".cjs", ".js", ".jsx", ".mjs"),
		configNames:      autoSet("package.json", "babel.config.js", "eslint.config.js", "prettier.config.js", "vite.config.js"),
	},
	"typescript": {
		markers:          autoSet("tsconfig.json"),
		sourceExtensions: autoSet(".cts", ".mts", ".ts", ".tsx"),
		configNames:      autoSet("tsconfig.json", "vite.config.ts"),
	},
	"vue": {
		markers:          autoSet("vue.config.js", "vue.config.ts"),
		sourceExtensions: autoSet(".vue"),
		configNames:      autoSet("vue.config.js", "vue.config.ts"),
	},
	"svelte": {
		markers:          autoSet("svelte.config.js", "svelte.config.ts"),
		sourceExtensions: autoSet(".svelte"),
		configNames:      autoSet("svelte.config.js", "svelte.config.ts"),
	},
	"nuxt": {
		markers:          autoSet("nuxt.config.js", "nuxt.config.ts"),
		sourceExtensions: autoSet(".vue"),
		configNames:      autoSet("nuxt.config.js", "nuxt.config.ts"),
	},
	"astro": {
		markers:          autoSet("astro.config.js", "astro.config.mjs", "astro.config.ts"),
		sourceExtensions: autoSet(".astro"),
		configNames:      autoSet("astro.config.js", "astro.config.mjs", "astro.config.ts"),
	},
	"angular": {
		markers:          autoSet("angular.json"),
		sourceExtensions: autoSet(".html", ".scss"),
		detectExtensions: autoSet(),
		configNames:      autoSet("angular.json"),
	},
	"python": {
		markers:          autoSet("pyproject.toml", "requirements.txt", "setup.py"),
		sourceExtensions: autoSet(".py", ".pyi"),
		configNames:      autoSet("pyproject.toml", "requirements.txt", "setup.py"),
	},
	"rust": {
		markers:          autoSet("cargo.toml"),
		sourceExtensions: autoSet(".rs"),
		configNames:      autoSet("cargo.toml"),
	},
	"jvm": {
		markers:          autoSet("build.gradle", "build.gradle.kts", "pom.xml", "settings.gradle", "settings.gradle.kts"),
		sourceExtensions: autoSet(".java", ".kt", ".kts"),
		configNames:      autoSet("build.gradle", "build.gradle.kts", "pom.xml", "settings.gradle", "settings.gradle.kts"),
	},
	"dotnet": {
		markers:          autoSet("global.json"),
		sourceExtensions: autoSet(".cs"),
		configNames:      autoSet("global.json"),
	},
	"ruby": {
		markers:          autoSet("gemfile"),
		sourceExtensions: autoSet(".rb"),
		configNames:      autoSet("gemfile"),
	},
	"php": {
		markers:          autoSet("composer.json"),
		sourceExtensions: autoSet(".php"),
		configNames:      autoSet("composer.json"),
	},
	"swift": {
		markers:          autoSet("package.swift"),
		sourceExtensions: autoSet(".swift"),
		configNames:      autoSet("package.swift"),
	},
	"dart": {
		markers:          autoSet("pubspec.yaml"),
		sourceExtensions: autoSet(".dart"),
		configNames:      autoSet("pubspec.yaml"),
	},
	"elixir": {
		markers:          autoSet("mix.exs"),
		sourceExtensions: autoSet(".ex", ".exs"),
		configNames:      autoSet("mix.exs"),
	},
	"shell": {
		sourceExtensions: autoSet(".bash", ".fish", ".sh", ".zsh"),
	},
	"infrastructure": {
		markers:          autoSet("dockerfile", "docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"),
		sourceExtensions: autoSet(".hcl", ".tf"),
		configNames:      autoSet("dockerfile", "docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"),
	},
}

var autoIndex = buildAutoStackIndex()

func buildAutoStackIndex() autoStackIndex {
	index := autoStackIndex{
		activationByExt:  make(map[string][]string),
		activationByName: make(map[string][]string),
		matchingByExt:    make(map[string][]string),
		matchingByName:   make(map[string][]string),
	}
	for name, stack := range autoStacks {
		for marker := range stack.markers {
			index.activationByName[marker] = append(index.activationByName[marker], name)
		}
		activationExtensions := stack.detectExtensions
		if activationExtensions == nil {
			activationExtensions = stack.sourceExtensions
		}
		for extension := range activationExtensions {
			index.activationByExt[extension] = append(index.activationByExt[extension], name)
		}
		for extension := range stack.sourceExtensions {
			index.matchingByExt[extension] = append(index.matchingByExt[extension], name)
		}
		for configName := range stack.configNames {
			index.matchingByName[configName] = append(index.matchingByName[configName], name)
		}
	}
	return index
}

func autoSet(values ...string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}

func selectAutoFiles(root string, ignoredDirs map[string]bool) ([]string, error) {
	files := make([]autoFile, 0)
	activeStacks := map[string]bool{}

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if entry.IsDir() {
			if path != root && isAutoIgnoredDir(entry.Name(), ignoredDirs) {
				return filepath.SkipDir
			}
			return nil
		}

		name := strings.ToLower(entry.Name())
		file := autoFile{path: path, name: name, ext: strings.ToLower(filepath.Ext(name))}
		files = append(files, file)
		activateStacksForFile(file, activeStacks, autoIndex)
		if name == "package.json" {
			activatePackageStacks(path, activeStacks)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	selected := make([]string, 0, len(files))
	for _, file := range files {
		if isAutoLockFile(file.name) {
			continue
		}

		if isAutoCommonFile(root, file.path) || matchesAutoStack(file, activeStacks, autoIndex) {
			selected = append(selected, file.path)
		}
	}

	sort.Strings(selected)
	return selected, nil
}

func isAutoIgnoredDir(name string, ignoredDirs map[string]bool) bool {
	name = strings.ToLower(name)
	return autoIgnoredDirs[name] || ignoredDirs[name]
}

func activateStacksForFile(file autoFile, activeStacks map[string]bool, index autoStackIndex) {
	for _, name := range index.activationByName[file.name] {
		activeStacks[name] = true
	}
	for _, name := range index.activationByExt[file.ext] {
		activeStacks[name] = true
	}

	if strings.HasSuffix(file.name, ".csproj") || strings.HasSuffix(file.name, ".sln") {
		activeStacks["dotnet"] = true
	}
}

func activatePackageStacks(path string, activeStacks map[string]bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	var manifest struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if json.Unmarshal(data, &manifest) != nil {
		return
	}

	for dependency := range manifest.Dependencies {
		activatePackageDependency(dependency, activeStacks)
	}
	for dependency := range manifest.DevDependencies {
		activatePackageDependency(dependency, activeStacks)
	}
}

func activatePackageDependency(dependency string, activeStacks map[string]bool) {
	switch strings.ToLower(dependency) {
	case "@angular/core":
		activeStacks["angular"] = true
	case "@astrojs/", "astro":
		activeStacks["astro"] = true
	case "@types/node", "typescript":
		activeStacks["typescript"] = true
	case "@sveltejs/kit", "svelte":
		activeStacks["svelte"] = true
	case "nuxt":
		activeStacks["nuxt"] = true
	case "@vitejs/plugin-vue", "vue":
		activeStacks["vue"] = true
	case "next", "react", "react-dom":
		activeStacks["typescript"] = true
		activeStacks["javascript"] = true
	}
}

func isAutoLockFile(name string) bool {
	return name == "bun.lockb" || name == "cargo.lock" || name == "go.sum" || name == "package-lock.json" || name == "pnpm-lock.yaml" || name == "yarn.lock"
}

func isAutoCommonFile(root, path string) bool {
	relativePath, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}

	if filepath.Dir(relativePath) == "." {
		name := strings.ToLower(filepath.Base(path))
		return strings.HasPrefix(name, "readme") || strings.HasPrefix(name, "contributing") || strings.HasPrefix(name, "license") || name == ".gitignore" || name == "taskfile.yml" || name == "taskfile.yaml"
	}

	return filepath.ToSlash(relativePath) != "" && strings.HasPrefix(filepath.ToSlash(relativePath), ".github/workflows/") && (strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml"))
}

func matchesAutoStack(file autoFile, activeStacks map[string]bool, index autoStackIndex) bool {
	for _, name := range index.matchingByExt[file.ext] {
		if activeStacks[name] {
			return true
		}
	}
	for _, name := range index.matchingByName[file.name] {
		if activeStacks[name] {
			return true
		}
	}

	return strings.HasSuffix(file.name, ".csproj") || strings.HasSuffix(file.name, ".sln")
}
