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

type toolDescriptor struct {
	ID    string
	Label string
}

var toolCatalog = []toolDescriptor{
	{ID: "blender", Label: "Blender"},
	{ID: "bun", Label: "Bun"},
	{ID: "cargo", Label: "Cargo"},
	{ID: "cmake", Label: "CMake"},
	{ID: "docker", Label: "Docker"},
	{ID: "docker-compose", Label: "Docker Compose"},
	{ID: "echo", Label: "Echo"},
	{ID: "ffmpeg", Label: "FFmpeg"},
	{ID: "git", Label: "Git"},
	{ID: "go", Label: "Go"},
	{ID: "hostname", Label: "Hostname"},
	{ID: "make", Label: "Make"},
	{ID: "mypy", Label: "mypy"},
	{ID: "ninja", Label: "Ninja"},
	{ID: "node", Label: "Node.js"},
	{ID: "npm", Label: "npm"},
	{ID: "ollama", Label: "Ollama"},
	{ID: "pnpm", Label: "pnpm"},
	{ID: "podman", Label: "Podman"},
	{ID: "poetry", Label: "Poetry"},
	{ID: "printf", Label: "printf"},
	{ID: "pytest", Label: "pytest"},
	{ID: "python", Label: "Python"},
	{ID: "python3", Label: "Python 3"},
	{ID: "ruff", Label: "Ruff"},
	{ID: "sh", Label: "Shell"},
	{ID: "sleep", Label: "Sleep"},
	{ID: "swift", Label: "Swift"},
	{ID: "uv", Label: "uv"},
	{ID: "xcodebuild", Label: "Xcode"},
	{ID: "yarn", Label: "Yarn"},
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
