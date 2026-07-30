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

// ToolIDs returns stable IDs for common command-line tools visible to this
// process. It performs path checks only; it does not execute discovered tools.
func ToolIDs() []string {
	candidates := []string{
		"blender",
		"bun",
		"cargo",
		"cmake",
		"docker",
		"docker-compose",
		"echo",
		"ffmpeg",
		"git",
		"go",
		"hostname",
		"make",
		"ninja",
		"node",
		"npm",
		"ollama",
		"pnpm",
		"podman",
		"printf",
		"python",
		"python3",
		"ruff",
		"sh",
		"sleep",
		"swift",
		"xcodebuild",
		"yarn",
	}
	found := make([]string, 0, len(candidates))
	for _, id := range candidates {
		if executableVisible(id) {
			found = append(found, id)
		}
	}
	slices.Sort(found)
	return found
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
