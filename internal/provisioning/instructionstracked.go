package provisioning

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// InstructionsDestTracked reports whether the project-scope instructions block
// of opts is written into a file git tracks (#1897): the destination itself
// (honouring opts.InstructionsRel) or, when it is a symlink, the file it
// resolves to inside the workspace. The block of a tracked file is shared with
// everyone who clones the repository, so the client sets
// ApplyOptions.OmitPathsTable from this answer: the "Local paths" table holds
// absolute paths of this machine and has no place there.
//
// It is decided once per projection, by the caller, and handed to Apply and to
// the status diff alike, so the recorded PathsSectionHash agrees with the one
// status recomputes. Any other scope, an empty destination, a workspace that is
// in no repository, and a repository git cannot read (TrackedPaths errors) all
// answer false: the block keeps its table, as before.
func InstructionsDestTracked(opts ApplyOptions) bool {
	if opts.Scope != contract.ScopeProject || opts.BaseDir == "" {
		return false
	}
	rel := instructionsDest(opts)
	if rel == "" {
		return false
	}
	tracked, err := TrackedPaths(opts.BaseDir)
	if err != nil || len(tracked) == 0 {
		return false
	}
	rel = filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel)))
	if tracked[rel] {
		return true
	}
	target, ok := workspaceLinkTarget(opts.BaseDir, rel)
	return ok && tracked[target]
}

// workspaceLinkTarget resolves rel (slash path under base) through its
// symlinks and returns where it lands as a slash path relative to base. ok is
// false when rel does not exist, is not a link, or lands outside base: the
// tracked set of a workspace says nothing about a file outside it.
func workspaceLinkTarget(base, rel string) (string, bool) {
	full := filepath.Join(base, filepath.FromSlash(rel))
	info, err := os.Lstat(full)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", false
	}
	root, err := filepath.EvalSymlinks(base)
	if err != nil {
		return "", false
	}
	out, err := filepath.Rel(root, resolved)
	if err != nil || out == ".." || strings.HasPrefix(out, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(out), true
}
