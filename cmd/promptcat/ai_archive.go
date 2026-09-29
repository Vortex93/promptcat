package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
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

type aiSymbolRecord struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Path       string `json:"path"`
	Language   string `json:"language"`
	Line       int    `json:"line"`
	Parent     string `json:"parent,omitempty"`
	Signature  string `json:"signature,omitempty"`
	Confidence string `json:"confidence"`
}

type aiImportRecord struct {
	Path         string `json:"path"`
	Language     string `json:"language"`
	Target       string `json:"target"`
	ResolvedPath string `json:"resolvedPath,omitempty"`
	Line         int    `json:"line"`
	Confidence   string `json:"confidence"`
}

type aiReferenceRecord struct {
	Name           string   `json:"name"`
	SymbolID       string   `json:"symbolId,omitempty"`
	CandidateIDs   []string `json:"candidateIds,omitempty"`
	Path           string   `json:"path"`
	Language       string   `json:"language"`
	Line           int      `json:"line"`
	Column         int      `json:"column"`
	Kind           string   `json:"kind"`
	SourceSymbolID string   `json:"sourceSymbolId,omitempty"`
	SourceSymbol   string   `json:"sourceSymbol,omitempty"`
	Confidence     string   `json:"confidence"`
}

type aiCallRecord struct {
	CallerID     string   `json:"callerId"`
	Caller       string   `json:"caller"`
	CalleeID     string   `json:"calleeId,omitempty"`
	CandidateIDs []string `json:"candidateIds,omitempty"`
	Callee       string   `json:"callee"`
	Path         string   `json:"path"`
	Line         int      `json:"line"`
	Confidence   string   `json:"confidence"`
}

type aiTestRecord struct {
	SymbolID     string   `json:"symbolId,omitempty"`
	CandidateIDs []string `json:"candidateIds,omitempty"`
	Symbol       string   `json:"symbol"`
	Path         string   `json:"path"`
	Line         int      `json:"line"`
	Confidence   string   `json:"confidence"`
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

type aiLanguageSummary struct {
	Files       int  `json:"files"`
	Symbols     int  `json:"symbols"`
	Imports     int  `json:"imports"`
	SymbolIndex bool `json:"symbolIndex"`
	ImportIndex bool `json:"importIndex"`
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

var aiRegexCache sync.Map

func aiRegexp(pattern string) *regexp.Regexp {
	if cached, ok := aiRegexCache.Load(pattern); ok {
		return cached.(*regexp.Regexp)
	}
	compiled := regexp.MustCompile(pattern)
	actual, _ := aiRegexCache.LoadOrStore(pattern, compiled)
	return actual.(*regexp.Regexp)
}

var identifierPattern = aiRegexp(`[A-Za-z_][A-Za-z0-9_]*`)

func writeAIArchivePack(root string, files []string, destination string) ([]string, error) {
	packDir := filepath.Join(destination, aiPackDirectory)
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		return nil, fmt.Errorf("create AI pack directory: %w", err)
	}

	languages := make(map[string]int)
	knownFiles := make(map[string]bool, len(files))
	for _, relative := range files {
		cleanRelative := filepath.ToSlash(filepath.Clean(relative))
		knownFiles[cleanRelative] = true
		languages[languageForPath(cleanRelative)]++
	}

	gitMetadata, changes := collectAIGitMetadata(root, knownFiles)
	repositories, err := collectAIRepositories(root, files)
	if err != nil {
		return nil, fmt.Errorf("collect repository metadata: %w", err)
	}
	patches, patchPaths, err := writeAIRepositoryPatches(root, files, repositories, packDir)
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
		Repositories:  repositories,
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

func writeJSONL[T any](path string, values []T) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", filepath.Base(path), err)
	}
	writer := bufio.NewWriter(file)
	encoder := json.NewEncoder(writer)
	for _, value := range values {
		if err := encoder.Encode(value); err != nil {
			_ = file.Close()
			return fmt.Errorf("write %s: %w", filepath.Base(path), err)
		}
	}
	if err := writer.Flush(); err != nil {
		_ = file.Close()
		return fmt.Errorf("flush %s: %w", filepath.Base(path), err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %s: %w", filepath.Base(path), err)
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

func languageHasImportIndex(language string) bool {
	switch language {
	case "go", "javascript", "typescript", "vue", "svelte", "python", "rust", "java", "kotlin", "csharp", "dart", "swift", "c", "cpp", "php", "ruby", "elixir", "shell", "lua", "scala", "groovy", "zig", "fsharp", "clojure", "perl", "r":
		return true
	default:
		return false
	}
}

func extractAISymbols(path, language, content string) []aiSymbolRecord {
	lines := strings.Split(content, "\n")
	symbols := make([]aiSymbolRecord, 0)
	currentParent := ""
	currentParentIndent := -1
	braceDepth := 0
	parentBraceDepth := -1

	add := func(name, kind string, line int, parent, signature, confidence string) {
		if name == "" || !identifierPattern.MatchString(name) {
			return
		}
		signature = strings.TrimSpace(signature)
		symbols = append(symbols, aiSymbolRecord{
			ID:         aiSymbolID(language, path, kind, parent, name, signature),
			Name:       name,
			Kind:       kind,
			Path:       path,
			Language:   language,
			Line:       line,
			Parent:     parent,
			Signature:  signature,
			Confidence: confidence,
		})
	}

	for index, line := range lines {
		lineNumber := index + 1
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))

		if language == "python" && currentParent != "" && indent <= currentParentIndent && !strings.HasPrefix(trimmed, "#") {
			currentParent = ""
			currentParentIndent = -1
		}
		if language != "python" && currentParent != "" && parentBraceDepth >= 0 && braceDepth < parentBraceDepth {
			currentParent = ""
			parentBraceDepth = -1
		}

		switch language {
		case "python":
			if match := aiRegexp(`^(?:async\s+)?def\s+([A-Za-z_]\w*)\s*\(`).FindStringSubmatch(trimmed); match != nil {
				kind := "function"
				parent := ""
				if currentParent != "" && indent > currentParentIndent {
					kind = "method"
					parent = currentParent
				}
				add(match[1], kind, lineNumber, parent, trimmed, "structured")
			} else if match := aiRegexp(`^class\s+([A-Za-z_]\w*)`).FindStringSubmatch(trimmed); match != nil {
				add(match[1], "class", lineNumber, "", trimmed, "structured")
				currentParent = match[1]
				currentParentIndent = indent
			} else if match := aiRegexp(`^([A-Z][A-Z0-9_]*)\s*=`).FindStringSubmatch(trimmed); match != nil {
				add(match[1], "constant", lineNumber, "", trimmed, "best_effort")
			}

		case "go":
			if match := aiRegexp(`^func\s+(?:\(([^)]*)\)\s*)?([A-Za-z_]\w*)\s*\(`).FindStringSubmatch(trimmed); match != nil {
				parent := goReceiverType(match[1])
				kind := "function"
				if parent != "" {
					kind = "method"
				}
				add(match[2], kind, lineNumber, parent, trimmed, "structured")
			} else if match := aiRegexp(`^type\s+([A-Za-z_]\w*)\s+(struct|interface)\b`).FindStringSubmatch(trimmed); match != nil {
				add(match[1], match[2], lineNumber, "", trimmed, "structured")
			} else if match := aiRegexp(`^type\s+([A-Za-z_]\w*)\b`).FindStringSubmatch(trimmed); match != nil {
				add(match[1], "type", lineNumber, "", trimmed, "structured")
			} else if match := aiRegexp(`^(const|var)\s+([A-Za-z_]\w*)\b`).FindStringSubmatch(trimmed); match != nil {
				kind := "variable"
				if match[1] == "const" {
					kind = "constant"
				}
				add(match[2], kind, lineNumber, "", trimmed, "structured")
			}

		case "javascript", "typescript", "vue", "svelte":
			if match := aiRegexp(`^(?:export\s+)?(?:default\s+)?(?:abstract\s+)?(class|interface|enum)\s+([A-Za-z_$][\w$]*)`).FindStringSubmatch(trimmed); match != nil {
				add(match[2], match[1], lineNumber, "", trimmed, "structured")
				currentParent = match[2]
				parentBraceDepth = braceDepth + strings.Count(line, "{") - strings.Count(line, "}")
				if parentBraceDepth <= braceDepth {
					parentBraceDepth = braceDepth + 1
				}
			} else if match := aiRegexp(`^(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s+([A-Za-z_$][\w$]*)\s*\(`).FindStringSubmatch(trimmed); match != nil {
				add(match[1], "function", lineNumber, "", trimmed, "structured")
			} else if match := aiRegexp(`^(?:export\s+)?(?:declare\s+)?type\s+([A-Za-z_$][\w$]*)\b`).FindStringSubmatch(trimmed); match != nil {
				add(match[1], "type", lineNumber, "", trimmed, "structured")
			} else if match := aiRegexp(`^(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*(?:async\s*)?(?:\([^)]*\)|[A-Za-z_$][\w$]*)\s*=>`).FindStringSubmatch(trimmed); match != nil {
				add(match[1], "function", lineNumber, "", trimmed, "structured")
			} else if match := aiRegexp(`^(?:export\s+)?(const|let|var)\s+([A-Za-z_$][\w$]*)\b`).FindStringSubmatch(trimmed); match != nil {
				kind := "variable"
				if match[1] == "const" {
					kind = "constant"
				}
				add(match[2], kind, lineNumber, "", trimmed, "structured")
			} else if currentParent != "" {
				if match := aiRegexp(`^(?:public\s+|private\s+|protected\s+|static\s+|readonly\s+|async\s+|get\s+|set\s+)*([A-Za-z_$][\w$]*)\s*\([^;]*\)\s*(?::[^={]+)?\s*\{?`).FindStringSubmatch(trimmed); match != nil && !isControlKeyword(match[1]) {
					add(match[1], "method", lineNumber, currentParent, trimmed, "best_effort")
				}
			}

		case "rust":
			if match := aiRegexp(`^(?:pub(?:\([^)]*\))?\s+)?(?:async\s+)?fn\s+([A-Za-z_]\w*)\s*`).FindStringSubmatch(trimmed); match != nil {
				kind := "function"
				if currentParent != "" {
					kind = "method"
				}
				add(match[1], kind, lineNumber, currentParent, trimmed, "structured")
			} else if match := aiRegexp(`^(?:pub(?:\([^)]*\))?\s+)?(struct|enum|trait|type)\s+([A-Za-z_]\w*)`).FindStringSubmatch(trimmed); match != nil {
				add(match[2], match[1], lineNumber, "", trimmed, "structured")
			} else if match := aiRegexp(`^impl(?:<[^>]+>)?\s+(?:[^\s]+\s+for\s+)?([A-Za-z_]\w*)`).FindStringSubmatch(trimmed); match != nil {
				currentParent = match[1]
				parentBraceDepth = braceDepth + strings.Count(line, "{") - strings.Count(line, "}")
				if parentBraceDepth <= braceDepth {
					parentBraceDepth = braceDepth + 1
				}
			} else if match := aiRegexp(`^(?:pub\s+)?(const|static)\s+([A-Za-z_]\w*)`).FindStringSubmatch(trimmed); match != nil {
				add(match[2], "constant", lineNumber, "", trimmed, "structured")
			}

		case "java", "kotlin", "csharp", "dart", "swift", "scala", "groovy":
			if match := aiRegexp(`^(?:[A-Za-z_@][\w@<>?,.\[\]\s]*\s+)?(class|interface|enum|struct|record|trait|object|protocol)\s+([A-Za-z_]\w*)`).FindStringSubmatch(trimmed); match != nil {
				add(match[2], match[1], lineNumber, "", trimmed, "structured")
				currentParent = match[2]
				parentBraceDepth = braceDepth + strings.Count(line, "{") - strings.Count(line, "}")
				if parentBraceDepth <= braceDepth {
					parentBraceDepth = braceDepth + 1
				}
			} else if name := cFamilyFunctionName(trimmed); name != "" {
				kind := "function"
				if currentParent != "" {
					kind = "method"
				}
				add(name, kind, lineNumber, currentParent, trimmed, "best_effort")
			}

		case "c", "cpp":
			if match := aiRegexp(`^(?:typedef\s+)?(struct|class|enum|union)\s+([A-Za-z_]\w*)`).FindStringSubmatch(trimmed); match != nil {
				add(match[2], match[1], lineNumber, "", trimmed, "structured")
				currentParent = match[2]
				parentBraceDepth = braceDepth + strings.Count(line, "{") - strings.Count(line, "}")
				if parentBraceDepth <= braceDepth {
					parentBraceDepth = braceDepth + 1
				}
			} else if name := cFamilyFunctionName(trimmed); name != "" {
				kind := "function"
				if strings.Contains(name, "::") {
					parts := strings.Split(name, "::")
					add(parts[len(parts)-1], "method", lineNumber, strings.Join(parts[:len(parts)-1], "::"), trimmed, "best_effort")
				} else {
					add(name, kind, lineNumber, currentParent, trimmed, "best_effort")
				}
			}

		case "php":
			if match := aiRegexp(`^(?:(?:abstract|final)\s+)?(class|interface|trait|enum)\s+([A-Za-z_]\w*)`).FindStringSubmatch(trimmed); match != nil {
				add(match[2], match[1], lineNumber, "", trimmed, "structured")
				currentParent = match[2]
				parentBraceDepth = braceDepth + strings.Count(line, "{") - strings.Count(line, "}")
				if parentBraceDepth <= braceDepth {
					parentBraceDepth = braceDepth + 1
				}
			} else if match := aiRegexp(`^(?:public\s+|private\s+|protected\s+|static\s+|final\s+|abstract\s+)*function\s+&?([A-Za-z_]\w*)\s*\(`).FindStringSubmatch(trimmed); match != nil {
				kind := "function"
				if currentParent != "" {
					kind = "method"
				}
				add(match[1], kind, lineNumber, currentParent, trimmed, "structured")
			}

		case "ruby":
			if match := aiRegexp(`^(class|module)\s+([A-Za-z_:][\w:]*)`).FindStringSubmatch(trimmed); match != nil {
				add(match[2], match[1], lineNumber, "", trimmed, "structured")
				currentParent = match[2]
				currentParentIndent = indent
			} else if match := aiRegexp(`^def\s+(?:self\.)?([A-Za-z_]\w*[!?=]?)`).FindStringSubmatch(trimmed); match != nil {
				kind := "function"
				if currentParent != "" {
					kind = "method"
				}
				add(match[1], kind, lineNumber, currentParent, trimmed, "structured")
			}

		case "elixir":
			if match := aiRegexp(`^defmodule\s+([A-Za-z_][\w.]*)`).FindStringSubmatch(trimmed); match != nil {
				add(match[1], "module", lineNumber, "", trimmed, "structured")
				currentParent = match[1]
			} else if match := aiRegexp(`^(def|defp|defmacro|defmacrop)\s+([A-Za-z_]\w*[!?]?)`).FindStringSubmatch(trimmed); match != nil {
				kind := "function"
				if match[1] == "defmacro" || match[1] == "defmacrop" {
					kind = "macro"
				}
				add(match[2], kind, lineNumber, currentParent, trimmed, "structured")
			}

		case "shell":
			if match := aiRegexp(`^(?:function\s+)?([A-Za-z_]\w*)\s*\(\)\s*\{?`).FindStringSubmatch(trimmed); match != nil {
				add(match[1], "function", lineNumber, "", trimmed, "structured")
			}

		case "lua":
			if match := aiRegexp(`^(?:local\s+)?function\s+([A-Za-z_][\w.:]*)\s*\(`).FindStringSubmatch(trimmed); match != nil {
				add(match[1], "function", lineNumber, "", trimmed, "structured")
			} else if match := aiRegexp(`^(?:local\s+)?([A-Za-z_]\w*)\s*=\s*function\s*\(`).FindStringSubmatch(trimmed); match != nil {
				add(match[1], "function", lineNumber, "", trimmed, "structured")
			}

		case "zig":
			if match := aiRegexp(`^(?:pub\s+)?fn\s+([A-Za-z_]\w*)\s*\(`).FindStringSubmatch(trimmed); match != nil {
				add(match[1], "function", lineNumber, "", trimmed, "structured")
			} else if match := aiRegexp(`^(?:pub\s+)?const\s+([A-Za-z_]\w*)\s*=\s*(struct|enum|union)`).FindStringSubmatch(trimmed); match != nil {
				add(match[1], match[2], lineNumber, "", trimmed, "structured")
			}

		case "fsharp":
			if match := aiRegexp(`^(?:let|and)\s+(?:inline\s+|rec\s+)?([A-Za-z_]\w*)`).FindStringSubmatch(trimmed); match != nil {
				add(match[1], "function", lineNumber, "", trimmed, "best_effort")
			} else if match := aiRegexp(`^type\s+([A-Za-z_]\w*)`).FindStringSubmatch(trimmed); match != nil {
				add(match[1], "type", lineNumber, "", trimmed, "structured")
			}

		case "clojure":
			if match := aiRegexp(`^\((defn?|defmacro|defprotocol|defrecord|deftype)\s+([^\s\[()]+)`).FindStringSubmatch(trimmed); match != nil {
				kind := "variable"
				if match[1] == "defn" {
					kind = "function"
				} else if match[1] == "defmacro" {
					kind = "macro"
				} else if strings.HasPrefix(match[1], "defp") || strings.HasPrefix(match[1], "defr") || strings.HasPrefix(match[1], "deft") {
					kind = "type"
				}
				add(match[2], kind, lineNumber, "", trimmed, "structured")
			}

		case "perl":
			if match := aiRegexp(`^sub\s+([A-Za-z_]\w*)`).FindStringSubmatch(trimmed); match != nil {
				add(match[1], "function", lineNumber, "", trimmed, "structured")
			} else if match := aiRegexp(`^package\s+([A-Za-z_][\w:]*)`).FindStringSubmatch(trimmed); match != nil {
				add(match[1], "module", lineNumber, "", trimmed, "structured")
			}

		case "r":
			if match := aiRegexp(`^([A-Za-z_.][\w.]*)\s*<-\s*function\s*\(`).FindStringSubmatch(trimmed); match != nil {
				add(match[1], "function", lineNumber, "", trimmed, "structured")
			}

		case "sql":
			if match := aiRegexp(`(?i)^create\s+(?:or\s+replace\s+)?(table|view|function|procedure|trigger|type)\s+(?:if\s+not\s+exists\s+)?([A-Za-z_][\w.$]*)`).FindStringSubmatch(trimmed); match != nil {
				add(match[2], strings.ToLower(match[1]), lineNumber, "", trimmed, "structured")
			}

		case "protobuf":
			if match := aiRegexp(`^(message|enum|service)\s+([A-Za-z_]\w*)`).FindStringSubmatch(trimmed); match != nil {
				add(match[2], match[1], lineNumber, "", trimmed, "structured")
			} else if match := aiRegexp(`^rpc\s+([A-Za-z_]\w*)\s*\(`).FindStringSubmatch(trimmed); match != nil {
				add(match[1], "method", lineNumber, "", trimmed, "structured")
			}

		case "graphql":
			if match := aiRegexp(`^(type|interface|input|enum|scalar|union|directive)\s+([A-Za-z_]\w*)`).FindStringSubmatch(trimmed); match != nil {
				add(match[2], match[1], lineNumber, "", trimmed, "structured")
			}

		case "terraform":
			if match := aiRegexp(`^(resource|data|module|variable|output)\s+"([^"]+)"(?:\s+"([^"]+)")?`).FindStringSubmatch(trimmed); match != nil {
				name := match[2]
				if match[3] != "" {
					name += "." + match[3]
				}
				add(name, match[1], lineNumber, "", trimmed, "structured")
			}
		}

		if language != "python" {
			braceDepth += strings.Count(line, "{") - strings.Count(line, "}")
			if currentParent != "" && parentBraceDepth >= 0 && braceDepth < parentBraceDepth {
				currentParent = ""
				parentBraceDepth = -1
			}
		}
	}

	return symbols
}

func goReceiverType(receiver string) string {
	receiver = strings.TrimSpace(receiver)
	if receiver == "" {
		return ""
	}
	fields := strings.Fields(receiver)
	if len(fields) == 0 {
		return ""
	}
	candidate := fields[len(fields)-1]
	candidate = strings.Trim(candidate, "*[]()")
	if index := strings.LastIndex(candidate, "."); index >= 0 {
		candidate = candidate[index+1:]
	}
	return candidate
}

func cFamilyFunctionName(line string) string {
	if strings.HasPrefix(line, "if ") || strings.HasPrefix(line, "if(") || strings.HasPrefix(line, "for ") || strings.HasPrefix(line, "for(") || strings.HasPrefix(line, "while ") || strings.HasPrefix(line, "while(") || strings.HasPrefix(line, "switch ") || strings.HasPrefix(line, "switch(") || strings.HasPrefix(line, "catch ") || strings.HasPrefix(line, "catch(") {
		return ""
	}
	match := aiRegexp(`(?:^|\s)([A-Za-z_~][\w:~]*)\s*\([^;{}]*\)\s*(?:const\s*)?(?:->[^\{]+)?\{?$`).FindStringSubmatch(line)
	if match == nil {
		return ""
	}
	return match[1]
}

func isControlKeyword(name string) bool {
	switch name {
	case "if", "for", "while", "switch", "catch", "constructor":
		return name != "constructor"
	default:
		return false
	}
}

func extractAIImports(path, language, content string) []aiImportRecord {
	lines := strings.Split(content, "\n")
	records := make([]aiImportRecord, 0)
	goImportBlock := false

	add := func(target string, line int, confidence string) {
		target = strings.TrimSpace(target)
		target = strings.Trim(target, "\"'`<> ")
		if target == "" {
			return
		}
		records = append(records, aiImportRecord{
			Path:       path,
			Language:   language,
			Target:     target,
			Line:       line,
			Confidence: confidence,
		})
	}

	for index, line := range lines {
		lineNumber := index + 1
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		switch language {
		case "go":
			if strings.HasPrefix(trimmed, "import (") || trimmed == "import(" || trimmed == "import (" {
				goImportBlock = true
				continue
			}
			if goImportBlock {
				if strings.HasPrefix(trimmed, ")") {
					goImportBlock = false
					continue
				}
				if match := aiRegexp("^(?:[._A-Za-z]\\w*\\s+)?[\"`]([^\"`]+)[\"`]").FindStringSubmatch(trimmed); match != nil {
					add(match[1], lineNumber, "structured")
				}
				continue
			}
			if match := aiRegexp("^import\\s+(?:[._A-Za-z]\\w*\\s+)?[\"`]([^\"`]+)[\"`]").FindStringSubmatch(trimmed); match != nil {
				add(match[1], lineNumber, "structured")
			}

		case "javascript", "typescript", "vue", "svelte":
			if match := aiRegexp(`\bfrom\s+["']([^"']+)["']`).FindStringSubmatch(trimmed); match != nil {
				add(match[1], lineNumber, "structured")
			} else if match := aiRegexp(`^import\s*["']([^"']+)["']`).FindStringSubmatch(trimmed); match != nil {
				add(match[1], lineNumber, "structured")
			}
			for _, pattern := range []*regexp.Regexp{
				aiRegexp(`\brequire\(\s*["']([^"']+)["']\s*\)`),
				aiRegexp(`\bimport\(\s*["']([^"']+)["']\s*\)`),
			} {
				if match := pattern.FindStringSubmatch(trimmed); match != nil {
					add(match[1], lineNumber, "structured")
				}
			}

		case "python":
			if match := aiRegexp(`^from\s+([.A-Za-z_]\w*(?:\.[A-Za-z_]\w*)*)\s+import\s+`).FindStringSubmatch(trimmed); match != nil {
				add(match[1], lineNumber, "structured")
			} else if match := aiRegexp(`^import\s+(.+)$`).FindStringSubmatch(trimmed); match != nil {
				for _, item := range strings.Split(match[1], ",") {
					fields := strings.Fields(strings.TrimSpace(item))
					if len(fields) > 0 {
						add(fields[0], lineNumber, "structured")
					}
				}
			}

		case "rust":
			if match := aiRegexp(`^(?:pub\s+)?use\s+([^;]+)`).FindStringSubmatch(trimmed); match != nil {
				add(match[1], lineNumber, "structured")
			} else if match := aiRegexp(`^mod\s+([A-Za-z_]\w*)\s*;`).FindStringSubmatch(trimmed); match != nil {
				add(match[1], lineNumber, "structured")
			}

		case "java", "kotlin", "dart", "scala", "groovy":
			if match := aiRegexp(`^import\s+(?:static\s+)?([^;]+)`).FindStringSubmatch(trimmed); match != nil {
				add(match[1], lineNumber, "structured")
			}

		case "csharp":
			if match := aiRegexp(`^(?:global\s+)?using\s+(?:[A-Za-z_]\w*\s*=\s*)?([^;]+);`).FindStringSubmatch(trimmed); match != nil {
				add(match[1], lineNumber, "structured")
			}

		case "swift":
			if match := aiRegexp(`^import\s+(?:class\s+|struct\s+|enum\s+|protocol\s+|func\s+|var\s+)?([A-Za-z_]\w*)`).FindStringSubmatch(trimmed); match != nil {
				add(match[1], lineNumber, "structured")
			}

		case "c", "cpp":
			if match := aiRegexp(`^#\s*include\s*[<"]([^>"]+)[>"]`).FindStringSubmatch(trimmed); match != nil {
				add(match[1], lineNumber, "structured")
			}

		case "php":
			if match := aiRegexp(`^use\s+([^;]+);`).FindStringSubmatch(trimmed); match != nil {
				add(match[1], lineNumber, "structured")
			} else if match := aiRegexp(`^(?:require|require_once|include|include_once)\s*\(?\s*["']([^"']+)["']`).FindStringSubmatch(trimmed); match != nil {
				add(match[1], lineNumber, "structured")
			}

		case "ruby":
			if match := aiRegexp(`^(?:require|require_relative|load)\s*[\("']+([^"')]+)`).FindStringSubmatch(trimmed); match != nil {
				add(match[1], lineNumber, "structured")
			}

		case "elixir":
			if match := aiRegexp(`^(?:alias|import|require|use)\s+([A-Za-z_][\w.]*)`).FindStringSubmatch(trimmed); match != nil {
				add(match[1], lineNumber, "structured")
			}

		case "shell":
			if match := aiRegexp(`^(?:source|\.)\s+([^\s#]+)`).FindStringSubmatch(trimmed); match != nil {
				add(match[1], lineNumber, "structured")
			}

		case "lua":
			if match := aiRegexp(`\brequire\s*\(?\s*["']([^"']+)["']`).FindStringSubmatch(trimmed); match != nil {
				add(match[1], lineNumber, "structured")
			}

		case "zig":
			if match := aiRegexp(`@import\(\s*"([^"]+)"\s*\)`).FindStringSubmatch(trimmed); match != nil {
				add(match[1], lineNumber, "structured")
			}

		case "fsharp":
			if match := aiRegexp(`^open\s+([A-Za-z_][\w.]*)`).FindStringSubmatch(trimmed); match != nil {
				add(match[1], lineNumber, "structured")
			}

		case "clojure":
			for _, match := range aiRegexp(`\[([A-Za-z_][\w.-]*(?:/[A-Za-z_][\w.-]*)?)`).FindAllStringSubmatch(trimmed, -1) {
				add(match[1], lineNumber, "best_effort")
			}

		case "perl":
			if match := aiRegexp(`^(?:use|require)\s+([A-Za-z_][\w:]*)`).FindStringSubmatch(trimmed); match != nil {
				add(match[1], lineNumber, "structured")
			}

		case "r":
			if match := aiRegexp(`^(?:library|require)\s*\(\s*["']?([^"')]+)`).FindStringSubmatch(trimmed); match != nil {
				add(match[1], lineNumber, "structured")
			}
		}
	}

	return deduplicateAIImports(records)
}

func deduplicateAIImports(records []aiImportRecord) []aiImportRecord {
	seen := map[string]bool{}
	result := make([]aiImportRecord, 0, len(records))
	for _, record := range records {
		key := fmt.Sprintf("%s:%d:%s", record.Path, record.Line, record.Target)
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, record)
	}
	return result
}

func resolveAIImport(sourcePath, language, target string, knownFiles map[string]bool) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return ""
	}

	candidates := make([]string, 0)
	sourceDir := filepath.ToSlash(filepath.Dir(filepath.FromSlash(sourcePath)))

	switch language {
	case "javascript", "typescript", "vue", "svelte":
		if !strings.HasPrefix(target, ".") {
			return ""
		}
		base := filepath.ToSlash(filepath.Clean(filepath.Join(filepath.FromSlash(sourceDir), filepath.FromSlash(target))))
		candidates = append(candidates, base)
		for _, extension := range []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".vue", ".svelte", ".json"} {
			candidates = append(candidates, base+extension)
			candidates = append(candidates, filepath.ToSlash(filepath.Join(filepath.FromSlash(base), "index"+extension)))
		}

	case "python":
		if !strings.HasPrefix(target, ".") {
			return ""
		}
		dots := len(target) - len(strings.TrimLeft(target, "."))
		module := strings.TrimLeft(target, ".")
		baseDir := filepath.FromSlash(sourceDir)
		for i := 1; i < dots; i++ {
			baseDir = filepath.Dir(baseDir)
		}
		modulePath := strings.ReplaceAll(module, ".", "/")
		base := filepath.ToSlash(filepath.Join(baseDir, filepath.FromSlash(modulePath)))
		candidates = append(candidates, base+".py", filepath.ToSlash(filepath.Join(filepath.FromSlash(base), "__init__.py")))

	case "c", "cpp", "shell", "php", "ruby":
		base := filepath.ToSlash(filepath.Clean(filepath.Join(filepath.FromSlash(sourceDir), filepath.FromSlash(target))))
		candidates = append(candidates, base)

	case "rust":
		if strings.Contains(target, "::") {
			first := strings.Split(target, "::")[0]
			candidates = append(candidates,
				filepath.ToSlash(filepath.Join(filepath.FromSlash(sourceDir), first+".rs")),
				filepath.ToSlash(filepath.Join(filepath.FromSlash(sourceDir), first, "mod.rs")),
			)
		} else {
			candidates = append(candidates,
				filepath.ToSlash(filepath.Join(filepath.FromSlash(sourceDir), target+".rs")),
				filepath.ToSlash(filepath.Join(filepath.FromSlash(sourceDir), target, "mod.rs")),
			)
		}
	}

	for _, candidate := range candidates {
		candidate = strings.TrimPrefix(filepath.ToSlash(filepath.Clean(candidate)), "./")
		if knownFiles[candidate] {
			return candidate
		}
	}
	return ""
}

func aiSymbolID(language, path, kind, parent, name, signature string) string {
	key := strings.Join([]string{language, filepath.ToSlash(path), kind, parent, name, signature}, "\x00")
	hash := sha256.Sum256([]byte(key))
	return fmt.Sprintf("sym_%x", hash[:8])
}

func classifyAIFile(path, language string) string {
	lower := strings.ToLower(filepath.ToSlash(path))
	base := strings.ToLower(filepath.Base(path))
	if isAITestPath(path) {
		return "test"
	}
	if strings.Contains(lower, "/generated/") || strings.Contains(lower, "/gen/") || strings.Contains(base, ".generated.") || strings.Contains(base, "_generated.") || strings.Contains(base, ".gen.") {
		return "generated"
	}
	if strings.Contains(lower, "/migrations/") || strings.Contains(lower, "/migration/") {
		return "migration"
	}
	if strings.HasSuffix(lower, ".md") || base == "readme" || strings.HasPrefix(base, "readme.") {
		return "documentation"
	}
	if language == "json" || language == "yaml" || language == "toml" || language == "xml" || strings.HasPrefix(base, ".") {
		return "config"
	}
	if languageHasSymbolIndex(language) {
		return "source"
	}
	return "other"
}

func isAITestPath(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))
	base := strings.ToLower(filepath.Base(path))
	return strings.Contains(lower, "/test/") ||
		strings.Contains(lower, "/tests/") ||
		strings.Contains(lower, "/__tests__/") ||
		strings.HasSuffix(base, "_test.go") ||
		strings.Contains(base, ".test.") ||
		strings.Contains(base, ".spec.") ||
		strings.HasPrefix(base, "test_") ||
		strings.HasSuffix(base, "_test.py")
}

func isAICallableSymbol(symbol aiSymbolRecord) bool {
	switch symbol.Kind {
	case "function", "method", "constructor":
		return true
	default:
		return false
	}
}

func buildAICodeRelationships(contents map[string]string, symbols []aiSymbolRecord, imports []aiImportRecord) ([]aiReferenceRecord, []aiCallRecord, []aiTestRecord) {
	symbolsByName := map[string][]aiSymbolRecord{}
	callablesByPath := map[string][]aiSymbolRecord{}
	definitions := map[string]bool{}
	importsByPath := map[string][]aiImportRecord{}
	languages := map[string]string{}

	for _, symbol := range symbols {
		symbolsByName[symbol.Name] = append(symbolsByName[symbol.Name], symbol)
		definitions[fmt.Sprintf("%s:%d:%s", symbol.Path, symbol.Line, symbol.Name)] = true
		languages[symbol.Path] = symbol.Language
		if isAICallableSymbol(symbol) {
			callablesByPath[symbol.Path] = append(callablesByPath[symbol.Path], symbol)
		}
	}
	for path := range callablesByPath {
		sort.Slice(callablesByPath[path], func(i, j int) bool {
			return callablesByPath[path][i].Line < callablesByPath[path][j].Line
		})
	}
	for _, item := range imports {
		importsByPath[item.Path] = append(importsByPath[item.Path], item)
	}

	references := make([]aiReferenceRecord, 0)
	calls := make([]aiCallRecord, 0)
	tests := make([]aiTestRecord, 0)
	seenReferences := map[string]bool{}
	seenCalls := map[string]bool{}
	seenTests := map[string]bool{}

	paths := make([]string, 0, len(contents))
	for path := range contents {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	for _, path := range paths {
		content := contents[path]
		language := languages[path]
		if language == "" {
			language = languageForPath(path)
		}
		lines := strings.Split(content, "\n")
		for index, line := range lines {
			lineNumber := index + 1
			for _, location := range identifierPattern.FindAllStringIndex(line, -1) {
				name := line[location[0]:location[1]]
				candidates := symbolsByName[name]
				if len(candidates) == 0 || definitions[fmt.Sprintf("%s:%d:%s", path, lineNumber, name)] {
					continue
				}

				candidates = narrowAIReferenceCandidates(path, candidates, importsByPath[path])
				kind := "reference"
				if looksLikeAICall(line, location[1]) {
					kind = "call"
				}
				source := enclosingAICallable(callablesByPath[path], lineNumber)
				reference := aiReferenceRecord{
					Name:       name,
					Path:       path,
					Language:   language,
					Line:       lineNumber,
					Column:     location[0] + 1,
					Kind:       kind,
					Confidence: "best_effort",
				}
				if len(candidates) == 1 {
					reference.SymbolID = candidates[0].ID
				} else {
					for _, candidate := range candidates {
						reference.CandidateIDs = append(reference.CandidateIDs, candidate.ID)
					}
				}
				if source != nil {
					reference.SourceSymbolID = source.ID
					reference.SourceSymbol = qualifiedAISymbolName(*source)
				}

				refKey := fmt.Sprintf("%s:%d:%d:%s:%s", path, lineNumber, location[0], name, kind)
				if !seenReferences[refKey] {
					seenReferences[refKey] = true
					references = append(references, reference)
				}

				if kind == "call" && source != nil {
					call := aiCallRecord{
						CallerID:   source.ID,
						Caller:     qualifiedAISymbolName(*source),
						Callee:     name,
						Path:       path,
						Line:       lineNumber,
						Confidence: "best_effort",
					}
					if len(candidates) == 1 {
						call.CalleeID = candidates[0].ID
					} else {
						for _, candidate := range candidates {
							call.CandidateIDs = append(call.CandidateIDs, candidate.ID)
						}
					}
					callKey := fmt.Sprintf("%s:%s:%d:%s", call.CallerID, call.Callee, lineNumber, path)
					if !seenCalls[callKey] {
						seenCalls[callKey] = true
						calls = append(calls, call)
					}
				}

				if isAITestPath(path) {
					test := aiTestRecord{
						Symbol:     name,
						Path:       path,
						Line:       lineNumber,
						Confidence: "best_effort",
					}
					if len(candidates) == 1 {
						test.SymbolID = candidates[0].ID
					} else {
						for _, candidate := range candidates {
							test.CandidateIDs = append(test.CandidateIDs, candidate.ID)
						}
					}
					testKey := fmt.Sprintf("%s:%d:%s", path, lineNumber, name)
					if !seenTests[testKey] {
						seenTests[testKey] = true
						tests = append(tests, test)
					}
				}
			}
		}
	}

	return references, calls, tests
}

func narrowAIReferenceCandidates(path string, candidates []aiSymbolRecord, imports []aiImportRecord) []aiSymbolRecord {
	local := make([]aiSymbolRecord, 0)
	for _, candidate := range candidates {
		if candidate.Path == path {
			local = append(local, candidate)
		}
	}
	if len(local) > 0 {
		return local
	}

	resolved := map[string]bool{}
	for _, item := range imports {
		if item.ResolvedPath != "" {
			resolved[item.ResolvedPath] = true
		}
	}
	imported := make([]aiSymbolRecord, 0)
	for _, candidate := range candidates {
		if resolved[candidate.Path] {
			imported = append(imported, candidate)
		}
	}
	if len(imported) > 0 {
		return imported
	}
	return candidates
}

func looksLikeAICall(line string, end int) bool {
	for end < len(line) && (line[end] == ' ' || line[end] == '\t') {
		end++
	}
	return end < len(line) && line[end] == '('
}

func enclosingAICallable(symbols []aiSymbolRecord, line int) *aiSymbolRecord {
	var current *aiSymbolRecord
	for index := range symbols {
		if symbols[index].Line > line {
			break
		}
		current = &symbols[index]
	}
	if current == nil || line-current.Line > 500 {
		return nil
	}
	return current
}

func qualifiedAISymbolName(symbol aiSymbolRecord) string {
	if symbol.Parent == "" {
		return symbol.Name
	}
	return symbol.Parent + "." + symbol.Name
}

func collectAIGitMetadata(root string, knownFiles map[string]bool) (aiGitMetadata, []aiChangeRecord) {
	archivedPaths := make(map[string]string, len(knownFiles))
	for path := range knownFiles {
		archivedPaths[path] = path
	}
	return collectAIGitMetadataMapped(root, archivedPaths)
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

func collectAIRepositories(root string, files []string) ([]aiRepositoryRecord, error) {
	repositoryRoots, err := discoverAIRepositoryRoots(root)
	if err != nil {
		return nil, err
	}
	filesByRepository := archiveFilesByRepository(root, files, repositoryRoots)

	repositories := make([]aiRepositoryRecord, 0, len(repositoryRoots))
	for _, repository := range repositoryRoots {
		repositoryFiles := filesByRepository[repository.root]
		if len(repositoryFiles) == 0 {
			continue
		}
		metadata, changes := collectAIGitMetadataMapped(repository.root, repositoryFiles)
		if !metadata.Available {
			continue
		}
		repositories = append(repositories, aiRepositoryRecord{
			Path:    repository.path,
			Git:     metadata,
			Changes: changes,
		})
	}
	return repositories, nil
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

func writeAIRepositoryPatches(root string, files []string, repositories []aiRepositoryRecord, packDir string) ([]aiPatchRecord, []string, error) {
	if len(repositories) == 0 {
		return nil, nil, nil
	}
	repositoryRoots, err := discoverAIRepositoryRoots(root)
	if err != nil {
		return nil, nil, err
	}
	rootsByPath := make(map[string]aiRepositoryRoot, len(repositoryRoots))
	for _, repository := range repositoryRoots {
		rootsByPath[repository.path] = repository
	}
	filesByRepository := archiveFilesByRepository(root, files, repositoryRoots)

	patchRecords := make([]aiPatchRecord, 0)
	patchPaths := make([]string, 0)
	for _, repository := range repositories {
		repositoryRoot, ok := rootsByPath[repository.Path]
		if !ok {
			continue
		}
		repositoryFiles := filesByRepository[repositoryRoot.root]
		pathspecs := make([]string, 0, len(repositoryFiles))
		for repositoryPath := range repositoryFiles {
			pathspecs = append(pathspecs, ":(literal)"+filepath.ToSlash(repositoryPath))
		}
		sort.Strings(pathspecs)
		if len(pathspecs) == 0 {
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

func discoverAIRepositoryRoots(root string) ([]aiRepositoryRoot, error) {
	ignored := make(map[string]bool, len(defaultArchiveIgnoredDirs))
	for _, name := range defaultArchiveIgnoredDirs {
		ignored[strings.ToLower(name)] = true
	}
	root = filepath.Clean(root)
	byRoot := make(map[string]aiRepositoryRoot)
	addRepository := func(repositoryRoot string) {
		repositoryRoot = filepath.Clean(repositoryRoot)
		relative, err := filepath.Rel(root, repositoryRoot)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			// The selected folder can be inside a repository. Represent that
			// enclosing repository at the selected archive root.
			relative = "."
		}
		path := filepath.ToSlash(relative)
		byRoot[repositoryRoot] = aiRepositoryRoot{path: path, root: repositoryRoot}
	}

	if repositoryRoot, err := runAIGit(root, "rev-parse", "--show-toplevel"); err == nil && repositoryRoot != "" {
		addRepository(repositoryRoot)
	}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root || !entry.IsDir() {
			return nil
		}
		if ignored[strings.ToLower(entry.Name())] {
			return filepath.SkipDir
		}
		marker := filepath.Join(path, ".git")
		if _, err := os.Lstat(marker); err == nil {
			if repositoryRoot, gitErr := runAIGit(path, "rev-parse", "--show-toplevel"); gitErr == nil && filepath.Clean(repositoryRoot) == filepath.Clean(path) {
				addRepository(repositoryRoot)
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk archive folder for repositories: %w", err)
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

func buildAIRepoMap(files []aiFileRecord, symbols []aiSymbolRecord) string {
	symbolsByFile := map[string][]aiSymbolRecord{}
	for _, symbol := range symbols {
		if symbol.Parent == "" && (symbol.Kind == "class" || symbol.Kind == "struct" || symbol.Kind == "interface" || symbol.Kind == "module" || symbol.Kind == "service" || symbol.Kind == "type") {
			symbolsByFile[symbol.Path] = append(symbolsByFile[symbol.Path], symbol)
		}
	}

	var builder strings.Builder
	builder.WriteString("# Promptcat repository map\n")
	builder.WriteString("# Generated before archiving. Paths are relative to the archive root.\n\n")
	for _, file := range files {
		fmt.Fprintf(&builder, "%s [%s]\n", file.Path, file.Language)
		top := symbolsByFile[file.Path]
		max := len(top)
		if max > 8 {
			max = 8
		}
		for i := 0; i < max; i++ {
			fmt.Fprintf(&builder, "  - %s %s (line %d)\n", top[i].Kind, top[i].Name, top[i].Line)
		}
		if len(top) > max {
			fmt.Fprintf(&builder, "  - ... %d more top-level symbols\n", len(top)-max)
		}
	}
	return builder.String()
}

const aiInstructions = `# Promptcat AI Navigation Pack

This archive uses a lazy navigation pack. Promptcat intentionally does not duplicate the repository into precomputed symbol/reference JSON indexes.

Use .promptcat/tools.py to scan the archived source on demand. The tool works with both archive layouts:
- default archives containing export.txt;
- --files archives containing real source files.

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

Most commands return compact text by default to reduce tokens. Commands that support structured output accept --json.

The scanner ignores generated .agentpack content by default and masks comments/string literals before semantic relationship scans to reduce false positives.
`
const aiToolsPython = `#!/usr/bin/env python3
import difflib
import json
import re
import sys
from pathlib import Path

PACK = Path(__file__).resolve().parent
ROOT = PACK.parent


def load_jsonl(name):
    path = PACK / name
    if not path.exists():
        return []
    rows = []
    with path.open("r", encoding="utf-8", errors="replace") as handle:
        for line in handle:
            line = line.strip()
            if line:
                rows.append(json.loads(line))
    return rows


def load_json(name):
    path = PACK / name
    if not path.exists():
        return {}
    return json.loads(path.read_text(encoding="utf-8", errors="replace"))


def export_files():
    path = ROOT / "export.txt"
    if not path.exists():
        return {}
    text = path.read_text(encoding="utf-8", errors="replace")
    marker = re.compile(r'^<<<FILE: (.+?)>>>\n(.*?)^<<<END FILE>>>$', re.M | re.S)
    result = {}
    for match in marker.finditer(text):
        raw_path = match.group(1)
        try:
            file_path = json.loads(raw_path)
        except Exception:
            file_path = raw_path.strip('"')
        content = match.group(2)
        if content.endswith("\n"):
            content = content[:-1]
        result[file_path.replace("\\", "/")] = content
    return result


def normalize_path(path):
    normalized = path.replace("\\", "/")
    while normalized.startswith("./"):
        normalized = normalized[2:]
    return normalized


def read_source(path):
    normalized = normalize_path(path)
    direct = ROOT / normalized
    if direct.is_file():
        return direct.read_text(encoding="utf-8", errors="replace")
    files = export_files()
    if normalized in files:
        return files[normalized]
    raise SystemExit(f"file not found in archive: {path}")


def print_rows(rows):
    for row in rows:
        print(json.dumps(row, ensure_ascii=False))


def qualified_symbol(row):
    parent = row.get("parent")
    return f"{parent}.{row.get('name', '')}" if parent else row.get("name", "")


def resolve_symbols(query, limit=8):
    rows = load_jsonl("symbols.jsonl")
    needle = query.lower()
    exact = [
        row for row in rows
        if row.get("id", "").lower() == needle
        or row.get("name", "").lower() == needle
        or qualified_symbol(row).lower() == needle
    ]
    if exact:
        return exact[:limit]

    scored = []
    for row in rows:
        name = row.get("name", "")
        qualified = qualified_symbol(row)
        path = row.get("path", "")
        score = max(
            difflib.SequenceMatcher(None, needle, name.lower()).ratio(),
            difflib.SequenceMatcher(None, needle, qualified.lower()).ratio(),
        )
        if needle in name.lower() or needle in qualified.lower():
            score += 0.35
        if needle in path.lower():
            score += 0.10
        score = min(1.0, score)
        if score >= 0.35:
            scored.append((score, row))
    scored.sort(key=lambda item: (-item[0], item[1].get("path", ""), item[1].get("line", 0)))
    result = []
    for score, row in scored[:limit]:
        item = dict(row)
        item["_score"] = round(score, 3)
        result.append(item)
    return result


def target_sets(query):
    symbols = resolve_symbols(query)
    ids = {row.get("id") for row in symbols if row.get("id")}
    names = {row.get("name") for row in symbols if row.get("name")}
    return symbols, ids, names


def row_matches_target(row, ids, names, id_key="symbolId", candidates_key="candidateIds", name_key="name"):
    if row.get(id_key) in ids:
        return True
    if ids.intersection(row.get(candidates_key) or []):
        return True
    return row.get(name_key) in names


def refs_for(query):
    _, ids, names = target_sets(query)
    return [row for row in load_jsonl("references.jsonl") if row_matches_target(row, ids, names)]


def callers_for(query):
    _, ids, names = target_sets(query)
    return [
        row for row in load_jsonl("calls.jsonl")
        if row_matches_target(row, ids, names, "calleeId", "candidateIds", "callee")
    ]


def callees_for(query):
    _, ids, _ = target_sets(query)
    return [row for row in load_jsonl("calls.jsonl") if row.get("callerId") in ids]


def tests_for(query):
    _, ids, names = target_sets(query)
    return [
        row for row in load_jsonl("tests.jsonl")
        if row_matches_target(row, ids, names, "symbolId", "candidateIds", "symbol")
    ]


def cmd_files(query=""):
    query = query.lower()
    rows = load_jsonl("files.jsonl")
    if query:
        rows = [row for row in rows if query in row.get("path", "").lower()]
    print_rows(rows)


def cmd_symbol(query):
    print_rows(resolve_symbols(query, 20))


def cmd_outline(path):
    normalized = normalize_path(path)
    rows = [row for row in load_jsonl("symbols.jsonl") if row.get("path") == normalized]
    print_rows(rows)


def cmd_imports(path):
    normalized = normalize_path(path)
    rows = [row for row in load_jsonl("imports.jsonl") if row.get("path") == normalized]
    print_rows(rows)


def dependent_rows(target):
    normalized = normalize_path(target)
    return [
        row for row in load_jsonl("imports.jsonl")
        if row.get("resolvedPath") == normalized or row.get("target") == target
    ]


def cmd_dependents(target):
    print_rows(dependent_rows(target))


def cmd_read(path, start=None, end=None):
    text = read_source(path)
    lines = text.splitlines()
    start = max(1, int(start or 1))
    end = min(len(lines), int(end or len(lines)))
    for index in range(start - 1, end):
        print(f"{index + 1:6} | {lines[index]}")


def cmd_search(query, prefix=""):
    prefix = normalize_path(prefix)
    records = sorted(
        load_jsonl("files.jsonl"),
        key=lambda row: (
            row.get("role") == "generated",
            row.get("role") == "other",
            row.get("path", ""),
        ),
    )
    for record in records:
        path = record.get("path", "")
        if prefix and not path.startswith(prefix):
            continue
        try:
            lines = read_source(path).splitlines()
        except Exception:
            continue
        for index, line in enumerate(lines, 1):
            if query.lower() in line.lower():
                print(f"{path}:{index}:{line.strip()}")


def cmd_refs(query):
    print_rows(refs_for(query))


def cmd_callers(query):
    print_rows(callers_for(query))


def cmd_callees(query):
    print_rows(callees_for(query))


def cmd_tests(query):
    print_rows(tests_for(query))


def cmd_git():
    print(json.dumps(load_json("git.json"), indent=2, ensure_ascii=False))


def cmd_changed():
    print_rows(load_jsonl("changes.jsonl"))


def cmd_changed_symbols():
    changed = {normalize_path(row.get("path", "")) for row in load_jsonl("changes.jsonl")}
    rows = [row for row in load_jsonl("symbols.jsonl") if normalize_path(row.get("path", "")) in changed]
    print_rows(rows)


def print_section(name, rows, limit=20):
    print(name)
    if not rows:
        print("  (none)")
        return
    for row in rows[:limit]:
        print(" ", json.dumps(row, ensure_ascii=False))
    if len(rows) > limit:
        print(f"  ... {len(rows) - limit} more")


def call_graph(symbol_ids, depth):
    calls = load_jsonl("calls.jsonl")
    visited = set(symbol_ids)
    frontier = set(symbol_ids)
    edges = []
    seen_edges = set()
    for _ in range(max(0, depth)):
        next_frontier = set()
        for row in calls:
            caller = row.get("callerId")
            callee = row.get("calleeId")
            candidates = row.get("candidateIds") or []
            related = caller in frontier or callee in frontier or bool(frontier.intersection(candidates))
            if not related:
                continue
            edge_key = (caller, callee, row.get("path"), row.get("line"), row.get("callee"))
            if edge_key not in seen_edges:
                seen_edges.add(edge_key)
                edges.append(row)
            if caller and caller not in visited:
                next_frontier.add(caller)
            if callee and callee not in visited:
                next_frontier.add(callee)
            for candidate in candidates:
                if candidate not in visited:
                    next_frontier.add(candidate)
        visited.update(next_frontier)
        frontier = next_frontier
        if not frontier:
            break
    return edges


def cmd_context(query, depth=1):
    symbols = resolve_symbols(query, 5)
    if not symbols:
        raise SystemExit(f"symbol not found: {query}")
    changes = {normalize_path(row.get("path", "")): row for row in load_jsonl("changes.jsonl")}

    for symbol in symbols:
        print("SYMBOL", json.dumps(symbol, ensure_ascii=False))
        path = symbol.get("path")
        line = int(symbol.get("line", 1))
        if path in changes:
            print("CHANGE", json.dumps(changes[path], ensure_ascii=False))
        print("SOURCE")
        cmd_read(path, max(1, line - 6), line + 16)
        print_section("CALLERS", callers_for(symbol.get("id") or symbol.get("name")))
        print_section("CALLEES", callees_for(symbol.get("id") or symbol.get("name")))
        print_section("REFERENCES", refs_for(symbol.get("id") or symbol.get("name")), 25)
        print_section("TESTS", tests_for(symbol.get("id") or symbol.get("name")))
        print_section("IMPORTS", [row for row in load_jsonl("imports.jsonl") if row.get("path") == path])
        print_section("DEPENDENTS", dependent_rows(path))
        if int(depth) > 1:
            print_section("RELATED CALL GRAPH", call_graph({symbol.get("id")}, int(depth)), 40)
        print()


def cmd_impact(query, depth=2):
    symbols = resolve_symbols(query, 8)
    if not symbols:
        raise SystemExit(f"symbol not found: {query}")
    ids = {row.get("id") for row in symbols if row.get("id")}
    refs = refs_for(query)
    callers = callers_for(query)
    callees = callees_for(query)
    tests = tests_for(query)
    graph = call_graph(ids, int(depth))
    changes = load_jsonl("changes.jsonl")
    changed_paths = {normalize_path(row.get("path", "")) for row in changes}

    affected = set()
    for row in symbols + refs + callers + callees + tests + graph:
        if row.get("path"):
            affected.add(normalize_path(row["path"]))
    for symbol in symbols:
        for row in dependent_rows(symbol.get("path", "")):
            if row.get("path"):
                affected.add(normalize_path(row["path"]))

    summary = {
        "query": query,
        "symbols": len(symbols),
        "references": len(refs),
        "directCallers": len(callers),
        "directCallees": len(callees),
        "tests": len(tests),
        "relatedCallEdges": len(graph),
        "affectedFiles": len(affected),
        "changedAffectedFiles": len(affected.intersection(changed_paths)),
    }
    print(json.dumps(summary, indent=2, ensure_ascii=False))
    print("AFFECTED FILES")
    for path in sorted(affected):
        marker = " *changed*" if path in changed_paths else ""
        print(f"  {path}{marker}")


def usage():
    print("""Promptcat archive navigation helper

Usage:
  tools.py files [query]
  tools.py symbol <name-or-id>
  tools.py outline <path>
  tools.py imports <path>
  tools.py dependents <path-or-import-target>
  tools.py read <path> [start-line] [end-line]
  tools.py search <text> [path-prefix]
  tools.py refs <name-or-id>
  tools.py callers <name-or-id>
  tools.py callees <name-or-id>
  tools.py tests <name-or-id>
  tools.py context <name-or-id> [depth]
  tools.py impact <name-or-id> [depth]
  tools.py changed
  tools.py changed-symbols
  tools.py git
  tools.py map
""")


def main():
    if len(sys.argv) < 2:
        usage()
        raise SystemExit(2)
    command = sys.argv[1]
    args = sys.argv[2:]
    if command == "files":
        cmd_files(args[0] if args else "")
    elif command == "symbol" and args:
        cmd_symbol(args[0])
    elif command == "outline" and args:
        cmd_outline(args[0])
    elif command == "imports" and args:
        cmd_imports(args[0])
    elif command == "dependents" and args:
        cmd_dependents(args[0])
    elif command == "read" and args:
        cmd_read(args[0], args[1] if len(args) > 1 else None, args[2] if len(args) > 2 else None)
    elif command == "search" and args:
        cmd_search(args[0], args[1] if len(args) > 1 else "")
    elif command == "refs" and args:
        cmd_refs(args[0])
    elif command == "callers" and args:
        cmd_callers(args[0])
    elif command == "callees" and args:
        cmd_callees(args[0])
    elif command == "tests" and args:
        cmd_tests(args[0])
    elif command == "context" and args:
        cmd_context(args[0], int(args[1]) if len(args) > 1 else 1)
    elif command == "impact" and args:
        cmd_impact(args[0], int(args[1]) if len(args) > 1 else 2)
    elif command == "changed":
        cmd_changed()
    elif command == "changed-symbols":
        cmd_changed_symbols()
    elif command == "git":
        cmd_git()
    elif command == "map":
        print((PACK / "repo-map.txt").read_text(encoding="utf-8", errors="replace"), end="")
    else:
        usage()
        raise SystemExit(2)


if __name__ == "__main__":
    main()
`
