// Package walker provides filesystem traversal with gitignore-style glob exclusion.
package walker

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Walk recursively traverses rootPath and returns absolute paths of all files
// whose extension is in supportedExts and that do not match any excludePattern.
//
// Glob matching rules:
//   - A pattern without a path separator matches against the file/dir name only (basename).
//   - A pattern with a path separator matches against the path relative to rootPath.
//   - A pattern ending in '/' matches directories only.
//   - '*'  matches any sequence of non-separator characters.
//   - '**' matches any sequence of characters including path separators.
//   - '?'  matches any single non-separator character.
func Walk(rootPath string, excludePatterns []string, supportedExts []string) ([]string, error) {
	// Resolve rootPath to an absolute path so returned paths are absolute.
	absRoot, err := filepath.Abs(rootPath)
	if err != nil {
		return nil, err
	}

	// Build a set of supported extensions for O(1) lookup.
	extSet := make(map[string]struct{}, len(supportedExts))
	for _, ext := range supportedExts {
		// Normalise: ensure the extension starts with a dot.
		if ext != "" && ext[0] != '.' {
			ext = "." + ext
		}
		extSet[strings.ToLower(ext)] = struct{}{}
	}

	var results []string

	err = filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			// Skip entries we cannot access.
			return nil
		}

		// Compute the path relative to the root for pattern matching.
		rel, err := filepath.Rel(absRoot, path)
		if err != nil {
			return nil
		}

		// Skip the root itself.
		if rel == "." {
			return nil
		}

		isDir := d.IsDir()
		name := d.Name()

		// Check each exclude pattern.
		for _, pattern := range excludePatterns {
			if matchesExclude(pattern, rel, name, isDir) {
				if isDir {
					return filepath.SkipDir
				}
				return nil
			}
		}

		// Only collect files (not directories).
		if isDir {
			return nil
		}

		// Filter by supported extension.
		ext := strings.ToLower(filepath.Ext(name))
		if _, ok := extSet[ext]; !ok {
			return nil
		}

		results = append(results, path)
		return nil
	})

	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	return results, nil
}

// matchesExclude reports whether the given filesystem entry matches the exclude pattern.
//
//   - pattern: the raw exclude pattern from config
//   - rel:     the path of the entry relative to the walk root (using OS separator)
//   - name:    the base name of the entry
//   - isDir:   whether the entry is a directory
func matchesExclude(pattern, rel, name string, isDir bool) bool {
	// A pattern ending in '/' matches directories only.
	dirOnly := strings.HasSuffix(pattern, "/")
	if dirOnly {
		if !isDir {
			return false
		}
		pattern = strings.TrimSuffix(pattern, "/")
	}

	// Normalise separators in the pattern and rel path to forward slashes for
	// consistent matching, then convert back to OS separators where needed.
	patternSlash := filepath.ToSlash(pattern)
	relSlash := filepath.ToSlash(rel)

	// If the pattern contains no path separator it is a basename-only pattern.
	if !strings.Contains(patternSlash, "/") {
		return globMatch(patternSlash, name)
	}

	// Pattern contains a path separator — match against the relative path.
	return globMatchPath(patternSlash, relSlash)
}

// globMatch matches a simple glob pattern (supporting *, ?, but NOT **)
// against a single path component (no separators).
func globMatch(pattern, name string) bool {
	matched, err := filepath.Match(pattern, name)
	if err != nil {
		// Invalid pattern — treat as no match.
		return false
	}
	return matched
}

// globMatchPath matches a glob pattern that may contain ** against a
// slash-separated relative path.
func globMatchPath(pattern, relPath string) bool {
	return doubleStarMatch(pattern, relPath)
}

// doubleStarMatch implements ** glob matching.
//
// Rules:
//   - '**' matches zero or more path segments (including none).
//   - '*'  matches any sequence of non-separator characters within a segment.
//   - '?'  matches any single non-separator character within a segment.
//
// The algorithm splits both the pattern and the path on '/' and uses
// recursive backtracking when a '**' segment is encountered.
func doubleStarMatch(pattern, path string) bool {
	patParts := strings.Split(pattern, "/")
	pathParts := strings.Split(path, "/")
	return matchParts(patParts, pathParts)
}

func matchParts(patParts, pathParts []string) bool {
	for len(patParts) > 0 {
		seg := patParts[0]

		if seg == "**" {
			// '**' can match zero or more path segments.
			// Try matching the rest of the pattern against every suffix of pathParts.
			rest := patParts[1:]
			for i := 0; i <= len(pathParts); i++ {
				if matchParts(rest, pathParts[i:]) {
					return true
				}
			}
			return false
		}

		// No path parts left but pattern still has non-** segments.
		if len(pathParts) == 0 {
			return false
		}

		// Match the current segment using filepath.Match (handles * and ?).
		matched, err := filepath.Match(seg, pathParts[0])
		if err != nil || !matched {
			return false
		}

		patParts = patParts[1:]
		pathParts = pathParts[1:]
	}

	// Pattern exhausted — path must also be exhausted.
	return len(pathParts) == 0
}
