package context

import (
	"context"

	"whirlwind/internal/model"
)

// CaptureWorkingTree overlays eligible local source on a pinned Git index.
// The returned commit remains the Git revision; transient working-tree hashes
// identify source that differs from that revision without changing Build's
// persisted, HEAD-only index behavior.
func CaptureWorkingTree(ctx context.Context, cwd string, head Index) (Index, error) {
	root, err := workingTreeRoot(cwd)
	if err != nil {
		return Index{}, err
	}
	changed, err := workingTreeChanges(ctx, cwd)
	if err != nil {
		return Index{}, err
	}
	known := uniquePaths(append(indexedPaths(head), changed...))
	ignored, err := ignoredGitPaths(ctx, cwd, known)
	if err != nil {
		return Index{}, err
	}
	idx := cloneWorkingTreeIndex(head)
	for _, path := range known {
		if err := overlayWorkingTreePath(ctx, root, path, ignored[path], head, &idx); err != nil {
			return Index{}, err
		}
	}
	return idx, nil
}

func indexedPaths(index Index) []string {
	paths := make([]string, 0, len(index.Files))
	for path := range index.Files {
		paths = append(paths, path)
	}
	return paths
}

func cloneWorkingTreeIndex(head Index) Index {
	idx := head
	idx.Files = make(map[string]Entry, len(head.Files))
	for path, entry := range head.Files {
		idx.Files[path] = entry
	}
	idx.WorkingTreeFiles = map[string]string{}
	idx.WorkingTreeDeleted = map[string]bool{}
	idx.WorkingTreeExcluded = map[string]bool{}
	return idx
}

func overlayWorkingTreePath(ctx context.Context, root, path string, ignored bool, head Index, index *Index) error {
	if !safeSourcePath(path) || ignored {
		delete(index.Files, path)
		if _, existed := head.Files[path]; existed {
			index.WorkingTreeExcluded[path] = true
		}
		return nil
	}
	entry, present, excluded, err := readWorkingTreeEntry(ctx, root, path)
	if err != nil {
		return err
	}
	if !present {
		delete(index.Files, path)
		if _, existed := head.Files[path]; existed {
			if excluded {
				index.WorkingTreeExcluded[path] = true
			} else {
				index.WorkingTreeDeleted[path] = true
			}
		}
		return nil
	}
	index.Files[path] = entry
	original, existed := head.Files[path]
	if !existed || entry.Text != original.Text {
		index.WorkingTreeFiles[path] = model.Fingerprint([]byte(entry.Text))
	}
	return nil
}
