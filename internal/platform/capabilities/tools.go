// Package capabilities detects conservative, secret-free local execution
// capabilities.
package capabilities

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

const CatalogSchemaVersion uint32 = 1

// ToolDescriptor describes one versioned, secret-free local executable
// capability that a worker may advertise to paired orchestrators.
type ToolDescriptor struct {
	ID            string
	Label         string
	Category      string
	SchemaVersion uint32
}

var toolCatalog = []ToolDescriptor{
	{ID: "blender", Label: "Blender", Category: "video", SchemaVersion: CatalogSchemaVersion},
	{ID: "bun", Label: "Bun", Category: "build", SchemaVersion: CatalogSchemaVersion},
	{ID: "cargo", Label: "Cargo", Category: "build", SchemaVersion: CatalogSchemaVersion},
	{ID: "cmake", Label: "CMake", Category: "build", SchemaVersion: CatalogSchemaVersion},
	{ID: "docker", Label: "Docker", Category: "container", SchemaVersion: CatalogSchemaVersion},
	{ID: "docker-compose", Label: "Docker Compose", Category: "container", SchemaVersion: CatalogSchemaVersion},
	{ID: "echo", Label: "Echo", Category: "utility", SchemaVersion: CatalogSchemaVersion},
	{ID: "ffmpeg", Label: "FFmpeg", Category: "video", SchemaVersion: CatalogSchemaVersion},
	{ID: "git", Label: "Git", Category: "build", SchemaVersion: CatalogSchemaVersion},
	{ID: "go", Label: "Go", Category: "build", SchemaVersion: CatalogSchemaVersion},
	{ID: "hostname", Label: "Hostname", Category: "utility", SchemaVersion: CatalogSchemaVersion},
	{ID: "make", Label: "Make", Category: "build", SchemaVersion: CatalogSchemaVersion},
	{ID: "mypy", Label: "mypy", Category: "test", SchemaVersion: CatalogSchemaVersion},
	{ID: "ninja", Label: "Ninja", Category: "build", SchemaVersion: CatalogSchemaVersion},
	{ID: "node", Label: "Node.js", Category: "build", SchemaVersion: CatalogSchemaVersion},
	{ID: "npm", Label: "npm", Category: "build", SchemaVersion: CatalogSchemaVersion},
	{ID: "ollama", Label: "Ollama", Category: "ai", SchemaVersion: CatalogSchemaVersion},
	{ID: "pnpm", Label: "pnpm", Category: "build", SchemaVersion: CatalogSchemaVersion},
	{ID: "podman", Label: "Podman", Category: "container", SchemaVersion: CatalogSchemaVersion},
	{ID: "poetry", Label: "Poetry", Category: "build", SchemaVersion: CatalogSchemaVersion},
	{ID: "printf", Label: "printf", Category: "utility", SchemaVersion: CatalogSchemaVersion},
	{ID: "pytest", Label: "pytest", Category: "test", SchemaVersion: CatalogSchemaVersion},
	{ID: "python", Label: "Python", Category: "build", SchemaVersion: CatalogSchemaVersion},
	{ID: "python3", Label: "Python 3", Category: "build", SchemaVersion: CatalogSchemaVersion},
	{ID: "ruff", Label: "Ruff", Category: "test", SchemaVersion: CatalogSchemaVersion},
	{ID: "sh", Label: "Shell", Category: "utility", SchemaVersion: CatalogSchemaVersion},
	{ID: "sleep", Label: "Sleep", Category: "utility", SchemaVersion: CatalogSchemaVersion},
	{ID: "swift", Label: "Swift", Category: "build", SchemaVersion: CatalogSchemaVersion},
	{ID: "uv", Label: "uv", Category: "build", SchemaVersion: CatalogSchemaVersion},
	{ID: "xcodebuild", Label: "Xcode", Category: "build", SchemaVersion: CatalogSchemaVersion},
	{ID: "yarn", Label: "Yarn", Category: "build", SchemaVersion: CatalogSchemaVersion},
}

// ToolCatalog returns the stable tool-capability catalog known by this build.
func ToolCatalog() []ToolDescriptor {
	return append([]ToolDescriptor(nil), toolCatalog...)
}

// CommonToolIDs returns the stable IDs this detector can advertise.
func CommonToolIDs() []string {
	ids := make([]string, 0, len(toolCatalog))
	for _, descriptor := range toolCatalog {
		ids = append(ids, descriptor.ID)
	}
	return ids
}

// ToolIDs returns stable IDs for common command-line tools visible to this
// process. It performs path checks only; it does not execute discovered tools.
func ToolIDs() []string {
	candidates := CommonToolIDs()
	found := make([]string, 0, len(candidates))
	for _, id := range candidates {
		if executableVisible(id) {
			found = append(found, id)
		}
	}
	slices.Sort(found)
	return found
}

// ToolLabel returns a user-facing name for a stable tool ID.
func ToolLabel(id string) string {
	normalized := strings.ToLower(strings.TrimSpace(id))
	for _, descriptor := range toolCatalog {
		if descriptor.ID == normalized {
			return descriptor.Label
		}
	}
	if normalized == "" {
		return "unknown tool"
	}
	return normalized
}

// ToolListLabel formats stable IDs for user-facing compatibility errors.
func ToolListLabel(ids []string) string {
	if len(ids) == 0 {
		return "the needed tools"
	}
	labels := make([]string, 0, len(ids))
	for _, id := range ids {
		if normalized := strings.TrimSpace(id); normalized != "" {
			labels = append(labels, ToolLabel(normalized))
		}
	}
	if len(labels) == 0 {
		return "the needed tools"
	}
	if len(labels) == 1 {
		return labels[0]
	}
	if len(labels) == 2 {
		return labels[0] + " and " + labels[1]
	}
	return strings.Join(labels[:len(labels)-1], ", ") + ", and " + labels[len(labels)-1]
}

func executableVisible(name string) bool {
	if _, err := exec.LookPath(name); err == nil {
		return true
	}
	for _, directory := range extraSearchDirectories() {
		if executableInDirectory(directory, name) {
			return true
		}
	}
	return false
}

func executableInDirectory(directory, name string) bool {
	if strings.TrimSpace(directory) == "" {
		return false
	}
	candidates := []string{name}
	if runtime.GOOS == "windows" && filepath.Ext(name) == "" {
		candidates = append(candidates, name+".exe", name+".bat", name+".cmd")
	}
	for _, candidate := range candidates {
		info, err := os.Stat(filepath.Join(directory, candidate))
		if err == nil && !info.IsDir() && executableMode(info.Mode()) {
			return true
		}
	}
	return false
}

func executableMode(mode os.FileMode) bool {
	if runtime.GOOS == "windows" {
		return true
	}
	return mode&0o111 != 0
}

func extraSearchDirectories() []string {
	directories := []string{
		"/opt/homebrew/bin",
		"/usr/local/bin",
		"/opt/local/bin",
		"/usr/bin",
		"/bin",
	}
	if runtime.GOOS == "windows" {
		programFiles := os.Getenv("ProgramFiles")
		if programFiles != "" {
			directories = append(directories,
				filepath.Join(programFiles, "Docker", "Docker", "resources", "bin"),
				filepath.Join(programFiles, "Git", "cmd"),
				filepath.Join(programFiles, "nodejs"),
			)
		}
	}
	return uniqueDirectories(directories)
}

func uniqueDirectories(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		clean := filepath.Clean(strings.TrimSpace(value))
		if clean == "." {
			continue
		}
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		result = append(result, clean)
	}
	return result
}
