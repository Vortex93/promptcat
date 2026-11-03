package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const aiPackDirectory = ".promptcat"

//go:embed ai_tools.py
var aiLazyToolsPython string

type aiFileRecord struct {
	Path     string `json:"path"`
	Language string `json:"language"`
	Role     string `json:"role"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
	Lines    int    `json:"lines"`
}

type aiChangeRecord struct {
	Path     string `json:"path"`
	Status   string `json:"status"`
	Staged   bool   `json:"staged"`
	Worktree bool   `json:"worktree"`
	Archived bool   `json:"archived"`
}

type aiGitMetadata struct {
	Available  bool   `json:"available"`
	Branch     string `json:"branch,omitempty"`
	Commit     string `json:"commit,omitempty"`
	Upstream   string `json:"upstream,omitempty"`
	BaseBranch string `json:"baseBranch,omitempty"`
	Dirty      bool   `json:"dirty"`
}

type aiProjectManifest struct {
	SchemaVersion int                  `json:"schemaVersion"`
	Generator     string               `json:"generator"`
	Version       string               `json:"version"`
	GeneratedAt   string               `json:"generatedAt"`
	Mode          string               `json:"mode"`
	FileCount     int                  `json:"fileCount"`
	Languages     map[string]int       `json:"languages"`
	Git           aiGitMetadata        `json:"git"`
	Changes       []aiChangeRecord     `json:"changes,omitempty"`
	Repositories  []aiRepositoryRecord `json:"repositories,omitempty"`
	Patches       []aiPatchRecord      `json:"patches,omitempty"`
}

type aiRepositoryRecord struct {
	Path    string           `json:"path"`
	Git     aiGitMetadata    `json:"git"`
	Changes []aiChangeRecord `json:"changes,omitempty"`
}

type aiPatchRecord struct {
	Path       string `json:"path"`
	Repository string `json:"repository"`
	Kind       string `json:"kind"`
	Commit     string `json:"commit,omitempty"`
}

func writeAIArchivePack(root string, files []string, destination string) ([]string, error) {
	packDir := filepath.Join(destination, aiPackDirectory)
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		return nil, fmt.Errorf("create AI pack directory: %w", err)
	}

	languages := make(map[string]int)
	for _, relative := range files {
		languages[languageForPath(filepath.ToSlash(filepath.Clean(relative)))]++
	}

	// Discover repositories and assign files once; reuse both for metadata and patches.
	context, err := collectAIRepositories(root, files)
	if err != nil {
		return nil, fmt.Errorf("collect repository metadata: %w", err)
	}
	var gitMetadata aiGitMetadata
	var changes []aiChangeRecord
	for _, repository := range context.records {
		if repository.Path == "." {
			gitMetadata, changes = repository.Git, repository.Changes
			break
		}
	}
	patches, patchPaths, err := writeAIRepositoryPatches(context, packDir)
	if err != nil {
		return nil, fmt.Errorf("collect repository patches: %w", err)
	}
	manifest := aiProjectManifest{
		SchemaVersion: 6,
		Generator:     "promptcat",
		Version:       version,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		Mode:          "lazy",
		FileCount:     len(files),
		Languages:     languages,
		Git:           gitMetadata,
		Changes:       changes,
		Repositories:  context.records,
		Patches:       patches,
	}

	if err := writeJSON(filepath.Join(packDir, "project.json"), manifest); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(packDir, "AI_INSTRUCTIONS.md"), []byte(aiInstructions), 0o644); err != nil {
		return nil, fmt.Errorf("write AI instructions: %w", err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "tools.py"), []byte(aiLazyToolsPython), 0o755); err != nil {
		return nil, fmt.Errorf("write AI tools: %w", err)
	}

	packFiles := []string{
		filepath.ToSlash(filepath.Join(aiPackDirectory, "AI_INSTRUCTIONS.md")),
		filepath.ToSlash(filepath.Join(aiPackDirectory, "tools.py")),
		filepath.ToSlash(filepath.Join(aiPackDirectory, "project.json")),
	}
	return append(packFiles, patchPaths...), nil
}
func writeJSON(path string, value any) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", filepath.Base(path), err)
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	encodeErr := encoder.Encode(value)
	closeErr := file.Close()
	if encodeErr != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), encodeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close %s: %w", filepath.Base(path), closeErr)
	}
	return nil
}

func languageForPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go"
	case ".js", ".mjs", ".cjs", ".jsx":
		return "javascript"
	case ".ts", ".tsx":
		return "typescript"
	case ".vue":
		return "vue"
	case ".svelte":
		return "svelte"
	case ".py":
		return "python"
	case ".rs":
		return "rust"
	case ".java":
		return "java"
	case ".kt", ".kts":
		return "kotlin"
	case ".cs":
		return "csharp"
	case ".dart":
		return "dart"
	case ".swift":
		return "swift"
	case ".c", ".h":
		return "c"
	case ".cc", ".cpp", ".hpp", ".hh", ".hxx":
		return "cpp"
	case ".php":
		return "php"
	case ".rb":
		return "ruby"
	case ".ex", ".exs":
		return "elixir"
	case ".sh", ".bash", ".zsh":
		return "shell"
	case ".lua":
		return "lua"
	case ".scala":
		return "scala"
	case ".groovy":
		return "groovy"
	case ".zig":
		return "zig"
	case ".fs", ".fsx":
		return "fsharp"
	case ".clj", ".cljs", ".cljc":
		return "clojure"
	case ".pl", ".pm":
		return "perl"
	case ".r":
		return "r"
	case ".sql":
		return "sql"
	case ".proto":
		return "protobuf"
	case ".graphql", ".gql":
		return "graphql"
	case ".tf":
		return "terraform"
	case ".json":
		return "json"
	case ".yaml", ".yml":
		return "yaml"
	case ".toml":
		return "toml"
	case ".xml":
		return "xml"
	case ".html", ".htm":
		return "html"
	case ".css", ".scss", ".sass", ".less":
		return "css"
	default:
		return "text"
	}
}

func languageHasSymbolIndex(language string) bool {
	switch language {
	case "go", "javascript", "typescript", "vue", "svelte", "python", "rust", "java", "kotlin", "csharp", "dart", "swift", "c", "cpp", "php", "ruby", "elixir", "shell", "lua", "scala", "groovy", "zig", "fsharp", "clojure", "perl", "r", "sql", "protobuf", "graphql", "terraform":
		return true
	default:
		return false
	}
}

func collectAIGitMetadataMapped(root string, archivedPaths map[string]string) (aiGitMetadata, []aiChangeRecord) {
	metadata := aiGitMetadata{}
	if _, err := runAIGit(root, "rev-parse", "--is-inside-work-tree"); err != nil {
		return metadata, nil
	}
	metadata.Available = true
	metadata.Branch, _ = runAIGit(root, "rev-parse", "--abbrev-ref", "HEAD")
	metadata.Commit, _ = runAIGit(root, "rev-parse", "HEAD")
	metadata.Upstream, _ = runAIGit(root, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	base, _ := runAIGit(root, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	metadata.BaseBranch = strings.TrimPrefix(base, "origin/")

	status, err := runAIGitRaw(root, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil || status == "" {
		return metadata, nil
	}
	changes := make([]aiChangeRecord, 0)
	entries := strings.Split(status, "\x00")
	for index := 0; index < len(entries); index++ {
		entry := entries[index]
		if len(entry) < 4 {
			continue
		}
		code := entry[:2]
		path := filepath.ToSlash(entry[3:])
		if (code[0] == 'R' || code[0] == 'C' || code[1] == 'R' || code[1] == 'C') && index+1 < len(entries) {
			index++ // -z emits the original path as a second NUL-delimited field.
		}
		archivePath, archived := archivedPaths[path]
		if !archived {
			continue
		}
		changes = append(changes, aiChangeRecord{
			Path:     archivePath,
			Status:   code,
			Staged:   code[0] != ' ' && code[0] != '?',
			Worktree: code[1] != ' ',
			Archived: true,
		})
	}
	metadata.Dirty = len(changes) > 0
	return metadata, changes
}

// aiRepositoryContext keeps data from one repository discovery pass. It lives only
// for the current export; no persistent cache or new index is needed.
type aiRepositoryContext struct {
	roots             []aiRepositoryRoot
	filesByRepository map[string]map[string]string
	records           []aiRepositoryRecord
}

func collectAIRepositories(root string, files []string) (aiRepositoryContext, error) {
	var context aiRepositoryContext
	var err error
	context.roots, err = discoverAIRepositoryRoots(root, files)
	if err != nil {
		return context, err
	}
	context.filesByRepository = archiveFilesByRepository(root, files, context.roots)
	for _, repository := range context.roots {
		repositoryFiles := context.filesByRepository[repository.root]
		if len(repositoryFiles) == 0 {
			continue
		}
		metadata, changes := collectAIGitMetadataMapped(repository.root, repositoryFiles)
		if !metadata.Available {
			continue
		}
		context.records = append(context.records, aiRepositoryRecord{
			Path: repository.path, Git: metadata, Changes: changes,
		})
	}
	return context, nil
}

func archiveFilesByRepository(root string, files []string, repositories []aiRepositoryRoot) map[string]map[string]string {
	filesByRepository := make(map[string]map[string]string, len(repositories))
	for _, file := range files {
		cleanPath := filepath.Clean(file)
		absoluteFile := filepath.Join(root, cleanPath)
		var owner aiRepositoryRoot
		ownerLength := -1
		for _, repository := range repositories {
			relative, err := filepath.Rel(repository.root, absoluteFile)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
				continue
			}
			if len(repository.root) > ownerLength {
				owner = repository
				ownerLength = len(repository.root)
			}
		}
		if ownerLength < 0 {
			continue
		}
		repositoryPath, err := filepath.Rel(owner.root, absoluteFile)
		if err != nil {
			continue
		}
		if filesByRepository[owner.root] == nil {
			filesByRepository[owner.root] = make(map[string]string)
		}
		filesByRepository[owner.root][filepath.Clean(repositoryPath)] = filepath.ToSlash(cleanPath)
	}
	return filesByRepository
}

func writeAIRepositoryPatches(context aiRepositoryContext, packDir string) ([]aiPatchRecord, []string, error) {
	if len(context.records) == 0 {
		return nil, nil, nil
	}
	rootsByPath := make(map[string]aiRepositoryRoot, len(context.roots))
	for _, repository := range context.roots {
		rootsByPath[repository.path] = repository
	}

	patchRecords := make([]aiPatchRecord, 0)
	patchPaths := make([]string, 0)
	for _, repository := range context.records {
		repositoryRoot, ok := rootsByPath[repository.Path]
		if !ok {
			continue
		}
		repositoryFiles := context.filesByRepository[repositoryRoot.root]
		pathspecs := make([]string, 0, len(repositoryFiles))
		for repositoryPath := range repositoryFiles {
			pathspecs = append(pathspecs, ":(literal)"+filepath.ToSlash(repositoryPath))
		}
		sort.Strings(pathspecs)
		if len(pathspecs) == 0 {
			continue
		}
		if repository.Git.Commit == "" {
			// A new Git repository has a symbolic HEAD but no commit. Its
			// source files are already included; there is no HEAD to diff.
			head, err := runAIGit(repositoryRoot.root, "symbolic-ref", "-q", "HEAD")
			if err != nil || head == "" {
				return nil, nil, fmt.Errorf("repository %s has no readable HEAD: %w", repository.Path, err)
			}
			if _, err := runAIGit(repositoryRoot.root, "show-ref", "--verify", "--quiet", head); err == nil {
				return nil, nil, fmt.Errorf("repository %s has an invalid HEAD commit", repository.Path)
			} else {
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
					return nil, nil, fmt.Errorf("check repository %s unborn HEAD: %w", repository.Path, err)
				}
			}
			continue
		}

		directory := aiRepositoryPatchDirectory(repository.Path)
		patchDirectory := filepath.Join(packDir, "patches", directory)
		base := resolveAIArchiveBase(repositoryRoot.root)
		if base != "" {
			commits, revErr := runAIGit(repositoryRoot.root, "rev-list", "--reverse", base+"..HEAD")
			if revErr != nil {
				return nil, nil, fmt.Errorf("list commits for repository %s: %w", repository.Path, revErr)
			}
			for _, commit := range strings.Fields(commits) {
				arguments := []string{"format-patch", "-1", "--stdout", "--binary", "--full-index", "--no-signature", commit, "--"}
				arguments = append(arguments, pathspecs...)
				patch, patchErr := runAIGitPatch(repositoryRoot.root, arguments...)
				if patchErr != nil {
					return nil, nil, fmt.Errorf("format commit %s for repository %s: %w", commit, repository.Path, patchErr)
				}
				if !bytes.Contains(patch, []byte("diff --git ")) {
					continue
				}
				name := commit + ".patch"
				path := filepath.Join(patchDirectory, name)
				if err := writeAIPatch(path, patch); err != nil {
					return nil, nil, err
				}
				archivePath := filepath.ToSlash(filepath.Join(aiPackDirectory, "patches", directory, name))
				patchPaths = append(patchPaths, archivePath)
				patchRecords = append(patchRecords, aiPatchRecord{Path: archivePath, Repository: repository.Path, Kind: "commit", Commit: commit})
			}
		}

		worktreePatch, patchErr := runAIGitPatch(repositoryRoot.root, append([]string{"diff", "--binary", "--full-index", "HEAD", "--"}, pathspecs...)...)
		if patchErr != nil {
			return nil, nil, fmt.Errorf("diff worktree for repository %s: %w", repository.Path, patchErr)
		}
		if len(worktreePatch) > 0 {
			const name = "worktree.patch"
			path := filepath.Join(patchDirectory, name)
			if err := writeAIPatch(path, worktreePatch); err != nil {
				return nil, nil, err
			}
			archivePath := filepath.ToSlash(filepath.Join(aiPackDirectory, "patches", directory, name))
			patchPaths = append(patchPaths, archivePath)
			patchRecords = append(patchRecords, aiPatchRecord{Path: archivePath, Repository: repository.Path, Kind: "worktree"})
		}
	}
	sort.Strings(patchPaths)
	sort.Slice(patchRecords, func(i, j int) bool { return patchRecords[i].Path < patchRecords[j].Path })
	return patchRecords, patchPaths, nil
}

func aiRepositoryPatchDirectory(repositoryPath string) string {
	if repositoryPath == "." {
		return "root"
	}
	return filepath.Join("repo", filepath.FromSlash(repositoryPath))
}

func resolveAIArchiveBase(repositoryRoot string) string {
	if base, err := runAIGit(repositoryRoot, "rev-parse", "--verify", "origin/HEAD^{commit}"); err == nil && base != "" {
		return base
	}
	if base, err := runAIGit(repositoryRoot, "rev-parse", "--verify", "@{upstream}^{commit}"); err == nil && base != "" {
		return base
	}
	return ""
}

func runAIGitPatch(root string, args ...string) ([]byte, error) {
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); !ok || args[0] != "diff" || exitErr.ExitCode() != 1 {
			return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
		}
	}
	return stdout.Bytes(), nil
}

func writeAIPatch(path string, patch []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create patch directory: %w", err)
	}
	if err := os.WriteFile(path, patch, 0o644); err != nil {
		return fmt.Errorf("write patch %s: %w", filepath.Base(path), err)
	}
	return nil
}

type aiRepositoryRoot struct {
	path string
	root string
}

// Inspect only directories containing exported files. This excludes ignored and
// --exclude-repo paths automatically, without walking the entire source tree.
func discoverAIRepositoryRoots(root string, files []string) ([]aiRepositoryRoot, error) {
	root = filepath.Clean(root)
	byRoot := make(map[string]aiRepositoryRoot)
	addRepository := func(repositoryRoot string) {
		repositoryRoot = filepath.Clean(repositoryRoot)
		relative, err := filepath.Rel(root, repositoryRoot)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			// A selected folder can be nested inside a Git repository.
			relative = "."
		}
		byRoot[repositoryRoot] = aiRepositoryRoot{path: filepath.ToSlash(relative), root: repositoryRoot}
	}
	if repositoryRoot, err := runAIGit(root, "rev-parse", "--show-toplevel"); err == nil && repositoryRoot != "" {
		addRepository(repositoryRoot)
	}

	visited := make(map[string]bool)
	for _, file := range files {
		directory := filepath.Dir(filepath.Join(root, filepath.Clean(file)))
		for directory != root {
			relative, err := filepath.Rel(root, directory)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
				break
			}
			if !visited[directory] {
				visited[directory] = true
				if _, err := os.Lstat(filepath.Join(directory, ".git")); err == nil {
					if repositoryRoot, err := runAIGit(directory, "rev-parse", "--show-toplevel"); err == nil && filepath.Clean(repositoryRoot) == directory {
						addRepository(repositoryRoot)
					}
				}
			}
			directory = filepath.Dir(directory)
		}
	}

	repositories := make([]aiRepositoryRoot, 0, len(byRoot))
	for _, repository := range byRoot {
		repositories = append(repositories, repository)
	}
	sort.Slice(repositories, func(i, j int) bool {
		if repositories[i].path == repositories[j].path {
			return repositories[i].root < repositories[j].root
		}
		return repositories[i].path < repositories[j].path
	})
	return repositories, nil
}

func runAIGit(root string, args ...string) (string, error) {
	output, err := runAIGitRaw(root, args...)
	return strings.TrimSpace(output), err
}

func runAIGitRaw(root string, args ...string) (string, error) {
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	output, err := command.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(output), "\r\n"), nil
}

const aiInstructions = `# Promptcat AI Navigation Pack

This archive uses a lazy navigation pack. Promptcat intentionally does not duplicate the repository into precomputed symbol/reference JSON indexes.

Use .promptcat/tools.py to scan the archived source on demand. The tool works with both archive layouts:
- default archives containing export.txt;
- --files archives containing real source files;
- --chatgpt ZIPs with real files and an inventory in .promptcat/files.json.

## Recommended workflow

1. Read .promptcat/project.json for lightweight project/language/Git metadata. Its repositories array describes every detected repository by archive-relative path; top-level git and changes fields remain for compatibility. Patch records list included per-commit and worktree patches under .promptcat/patches/.
2. Start with targeted commands instead of recursively reading the repository.
3. Prefer symbol/context/search before opening whole files.
4. Use refs/callers/callees/tests/impact only when the task needs relationship information; they are computed lazily.
5. Treat relationship results as best-effort navigation hints across dynamic/ambiguous languages.

## Commands

    python .promptcat/tools.py files [query]
    python .promptcat/tools.py map [query]
    python .promptcat/tools.py symbol <name>
    python .promptcat/tools.py outline <path>
    python .promptcat/tools.py imports <path>
    python .promptcat/tools.py dependents <path-or-import-target>
    python .promptcat/tools.py read <path> [start-line] [end-line]
    python .promptcat/tools.py search <text> [path-prefix]
    python .promptcat/tools.py refs <symbol>
    python .promptcat/tools.py callers <symbol>
    python .promptcat/tools.py callees <symbol>
    python .promptcat/tools.py tests <symbol>
    python .promptcat/tools.py context <symbol>
    python .promptcat/tools.py impact <symbol>
    python .promptcat/tools.py changed
    python .promptcat/tools.py changed-symbols
    python .promptcat/tools.py git

For --chatgpt ZIPs, start with START_HERE.md at the ZIP root. The inventory allows
files/read/search to avoid loading unrelated source files. files and map support
--limit N --offset N for pagination. read defaults to at most 80 lines; provide
start/end lines or --source-lines N to expand it. Source ranges also default to
16,000 output characters (--max-chars N to expand); long search lines are marked
previews. Read-limit and pagination notices
are printed to stderr so --json stdout remains machine-readable.

Most commands return compact text by default to reduce tokens. Commands that support structured output accept --json.

The scanner ignores generated .agentpack content by default and masks comments/string literals before semantic relationship scans to reduce false positives.
`
