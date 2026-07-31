package snapshot

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	maximumIgnoreBytes = 1 << 20
	maximumIgnoreFiles = 256
	maximumIgnoreRules = 8_192
)

type ignoreRule struct {
	base      string
	pattern   string
	negative  bool
	directory bool
	hasSlash  bool
}

type ignoreMatcher struct {
	rules []ignoreRule
}

var defaultIgnoreRules = []ignoreRule{
	{pattern: ".git", directory: true},
	{pattern: ".computehop-results", directory: true},
	{pattern: ".computehop-conflicts", directory: true},

	// Common generated dependency, cache, and build-output folders. These are
	// expensive to transfer and are normally recreated on the worker.
	{pattern: "node_modules", directory: true},
	{pattern: ".next", directory: true},
	{pattern: ".turbo", directory: true},
	{pattern: ".cache", directory: true},
	{pattern: "target", directory: true},
	{pattern: ".venv", directory: true},
	{pattern: "venv", directory: true},
	{pattern: "__pycache__", directory: true},
	{pattern: ".pytest_cache", directory: true},
	{pattern: ".mypy_cache", directory: true},
	{pattern: ".ruff_cache", directory: true},

	// Common local credential files. A project can deliberately opt specific
	// non-secret examples back in with .computehopignore negations.
	{pattern: ".env"},
	{pattern: ".env.*"},
	{pattern: ".npmrc"},
	{pattern: ".pypirc"},
	{pattern: ".netrc"},
	{pattern: ".ssh", directory: true},
	{pattern: ".aws", directory: true},
	{pattern: ".azure", directory: true},
	{pattern: ".gcloud", directory: true},
	{pattern: ".gnupg", directory: true},
	{pattern: ".kube", directory: true},
	{pattern: "*.pem"},
	{pattern: "*.key"},
	{pattern: "*.p12"},
	{pattern: "*.pfx"},
}

func loadIgnoreMatcher(root string) (ignoreMatcher, error) {
	matcher := ignoreMatcher{rules: append([]ignoreRule(nil), defaultIgnoreRules...)}
	ignoreFiles := 0
	err := filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if !entry.IsDir() {
			return nil
		}
		if relative != "." {
			if matcher.ignored(relative, true) {
				return filepath.SkipDir
			}
		}
		base := relative
		if base == "." {
			base = ""
		}
		for _, name := range []string{".gitignore", ".computehopignore"} {
			filename := filepath.Join(current, name)
			info, err := os.Lstat(filename)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				continue
			}
			ignoreFiles++
			if ignoreFiles > maximumIgnoreFiles {
				return errors.New("project contains too many ignore files")
			}
			file, err := os.Open(filename)
			if err != nil {
				return err
			}
			rules, readErr := parseIgnoreRules(file, base)
			closeErr := file.Close()
			relativeSource := path.Join(base, name)
			if readErr != nil {
				return fmt.Errorf("read %s: %w", relativeSource, readErr)
			}
			if closeErr != nil {
				return fmt.Errorf("close %s: %w", relativeSource, closeErr)
			}
			if len(matcher.rules)+len(rules) > maximumIgnoreRules+len(defaultIgnoreRules) {
				return errors.New("project contains too many ignore rules")
			}
			matcher.rules = append(matcher.rules, rules...)
		}
		return nil
	})
	return matcher, err
}

func parseIgnoreRules(reader io.Reader, base string) ([]ignoreRule, error) {
	contents, err := io.ReadAll(io.LimitReader(reader, maximumIgnoreBytes+1))
	if err != nil {
		return nil, err
	}
	if len(contents) > maximumIgnoreBytes {
		return nil, errors.New("ignore file exceeds size limit")
	}
	scanner := bufio.NewScanner(bytes.NewReader(contents))
	scanner.Buffer(make([]byte, 64<<10), maximumIgnoreBytes+1)
	var rules []ignoreRule
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		line = trimUnescapedTrailingSpaces(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		escapedLeadingMarker := strings.HasPrefix(line, `\#`) || strings.HasPrefix(line, `\!`)
		if escapedLeadingMarker {
			line = line[1:]
		}
		rule := ignoreRule{base: base}
		if !escapedLeadingMarker && strings.HasPrefix(line, "!") {
			rule.negative = true
			line = strings.TrimPrefix(line, "!")
		}
		rule.directory = strings.HasSuffix(line, "/")
		line = strings.TrimSuffix(line, "/")
		line = strings.TrimPrefix(line, "/")
		if line == "" {
			continue
		}
		rule.pattern = line
		rule.hasSlash = strings.Contains(line, "/")
		rules = append(rules, rule)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return rules, nil
}

func trimUnescapedTrailingSpaces(value string) string {
	for strings.HasSuffix(value, " ") && !strings.HasSuffix(value, `\ `) {
		value = strings.TrimSuffix(value, " ")
	}
	return strings.ReplaceAll(value, `\ `, " ")
}

func (matcher ignoreMatcher) ignored(relative string, directory bool) bool {
	// Internal metadata and returned results are never eligible for transfer,
	// even if a later user rule attempts to negate the exclusion.
	for _, segment := range strings.Split(relative, "/") {
		if segment == ".git" || segment == ".computehop-results" || segment == ".computehop-conflicts" {
			return true
		}
	}
	ignored := false
	for _, rule := range matcher.rules {
		candidate, ok := relativeToBase(relative, rule.base)
		if !ok || (rule.directory && !directory) {
			continue
		}
		matched := false
		if rule.hasSlash {
			matched = matchGlob(rule.pattern, candidate)
		} else {
			segments := strings.Split(candidate, "/")
			for _, segment := range segments {
				if matchSegment(rule.pattern, segment) {
					matched = true
					break
				}
			}
		}
		if matched {
			ignored = !rule.negative
		}
	}
	return ignored
}

func relativeToBase(value, base string) (string, bool) {
	if base == "" {
		return value, true
	}
	if value == base {
		return "", true
	}
	if strings.HasPrefix(value, base+"/") {
		return strings.TrimPrefix(value, base+"/"), true
	}
	return "", false
}

func matchGlob(pattern, value string) bool {
	patternSegments := strings.Split(pattern, "/")
	valueSegments := strings.Split(value, "/")
	var match func(int, int) bool
	match = func(patternIndex, valueIndex int) bool {
		if patternIndex == len(patternSegments) {
			return valueIndex == len(valueSegments)
		}
		if patternSegments[patternIndex] == "**" {
			for next := valueIndex; next <= len(valueSegments); next++ {
				if match(patternIndex+1, next) {
					return true
				}
			}
			return false
		}
		return valueIndex < len(valueSegments) &&
			matchSegment(patternSegments[patternIndex], valueSegments[valueIndex]) &&
			match(patternIndex+1, valueIndex+1)
	}
	return match(0, 0)
}

func matchSegment(pattern, value string) bool {
	matched, err := path.Match(pattern, value)
	return err == nil && matched
}
