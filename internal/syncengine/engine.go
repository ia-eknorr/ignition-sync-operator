package syncengine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SyncResult holds the outcome of a sync operation.
type SyncResult struct {
	FilesAdded     int
	FilesModified  int
	FilesDeleted   int
	ProjectsSynced []string
	Duration       time.Duration
	DryRunDiff     *DryRunDiff
}

// Engine handles syncing files from a source directory to a destination directory.
type Engine struct {
	ExcludePatterns []string
}

// Sync copies files from srcRoot to dstRoot, applying exclude patterns,
// protecting .resources/ directories, and removing orphaned files.
// Returns a SyncResult with counts of changes made.
func (e *Engine) Sync(srcRoot, dstRoot string) (*SyncResult, error) {
	start := time.Now()
	result := &SyncResult{}

	excludes := MergeExcludes(e.ExcludePatterns)

	// Phase 1: Walk source, copy new/changed files to destination.
	srcFiles := make(map[string]bool)

	err := filepath.Walk(srcRoot, func(srcPath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(srcRoot, srcPath)
		if err != nil {
			return err
		}
		if relPath == "." {
			return nil
		}

		// Check excludes.
		if ShouldExclude(relPath, excludes) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Check protected patterns (don't sync .resources/ from source).
		if IsProtected(relPath) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if info.IsDir() {
			srcFiles[relPath+"/"] = true
			dstPath := filepath.Join(dstRoot, relPath)
			return os.MkdirAll(dstPath, 0755)
		}

		// Regular file — track it and copy if changed.
		srcFiles[relPath] = true
		dstPath := filepath.Join(dstRoot, relPath)

		// Check existence before copy so we can distinguish added vs modified.
		_, existErr := os.Stat(dstPath)
		existed := existErr == nil

		written, err := copyFile(srcPath, dstPath)
		if err != nil {
			return fmt.Errorf("syncing %s: %w", relPath, err)
		}

		if written {
			if existed {
				result.FilesModified++
			} else {
				result.FilesAdded++
			}
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking source %s: %w", srcRoot, err)
	}

	// Phase 2: Walk destination, delete orphaned files not in source.
	err = filepath.Walk(dstRoot, func(dstPath string, info os.FileInfo, err error) error {
		if err != nil {
			// Directory may have been removed by a parent deletion.
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}

		relPath, err := filepath.Rel(dstRoot, dstPath)
		if err != nil {
			return err
		}
		if relPath == "." {
			return nil
		}

		// Never delete protected paths.
		if IsProtected(relPath) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Don't delete excluded paths (they were never candidates for sync).
		if ShouldExclude(relPath, excludes) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if info.IsDir() {
			// Check if this directory exists in source.
			if !srcFiles[relPath+"/"] {
				// Check if any source file is under this directory.
				hasSourceChildren := false
				for sf := range srcFiles {
					if strings.HasPrefix(sf, relPath+"/") {
						hasSourceChildren = true
						break
					}
				}
				if !hasSourceChildren {
					// This directory has no source counterpart — skip it entirely.
					// Don't delete directories that weren't part of the sync scope.
					return filepath.SkipDir
				}
			}
			return nil
		}

		// Regular file: delete if not in source set.
		if !srcFiles[relPath] {
			if err := os.Remove(dstPath); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("removing orphan %s: %w", relPath, err)
			}
			result.FilesDeleted++
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("cleaning orphans in %s: %w", dstRoot, err)
	}

	// Discover synced projects by looking at top-level dirs under projects/.
	result.ProjectsSynced = discoverProjects(dstRoot)
	result.Duration = time.Since(start)

	return result, nil
}

// discoverProjects lists directories under dstRoot/projects/.
func discoverProjects(dstRoot string) []string {
	projectsDir := filepath.Join(dstRoot, "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil
	}

	var projects []string
	for _, e := range entries {
		if e.IsDir() {
			projects = append(projects, e.Name())
		}
	}
	return projects
}
