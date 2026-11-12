package gitx

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// indexLockRe is git's refusal to take the index lock (LC_ALL=C, see gitEnv): the
// text names the lock file whatever the platform's path separators.
var indexLockRe = regexp.MustCompile(`Unable to create '([^']*index\.lock)': File exists`)

// indexLockHint turns git's raw "Unable to create '.../index.lock': File exists"
// into a sentence for the operator, to append to the error: it names the file and
// says when it is safe to remove. "" when out is not that refusal. The lock may
// belong to a git the operator is running in that clone, so the server never removes
// it on a write (RemoveStaleIndexLock is the one removal, at start-up).
func indexLockHint(dir, out string) string {
	m := indexLockRe.FindStringSubmatch(out)
	if m == nil {
		return ""
	}
	return fmt.Sprintf("\ncartographer: %s is a lock left by a git that did not finish: if no git process runs in %s, remove that file", m[1], dir)
}

// abortRebaseIfStarted aborts the rebase a refused pull or rebase may have begun,
// best effort. When git could not take the index lock it usually stopped before
// creating any rebase state ("could not detach HEAD"): then there is nothing to
// abort, and `rebase --abort` would only fail on the same lock. Only a state
// directory actually present is aborted, so a refusal never runs it blindly.
func abortRebaseIfStarted(dir string) {
	if _, ok := RebaseInProgress(dir); ok {
		_, _ = runGit(dir, "rebase", "--abort")
	}
}

// RemoveStaleIndexLock removes the index.lock a crash, a power loss or a kill left
// in the repository at dir, and returns its path and age (path "" when there was
// none). The caller must hold the KB's exclusive process lock and run it before
// serving: only then no git of this server runs in the clone, so a lock found is
// a leftover. dir must be the root of its own repository or linked worktree (a
// `.git` directory or file in it), never a folder inside a larger one, whose lock
// is not ours to clear: a bogus or empty `.git` there makes git walk up to the
// enclosing repository, so the top level git reports must be dir itself. The lock
// path comes from git itself (rev-parse --git-path), so a worktree or a `.git`
// file pointing elsewhere resolves to the right git dir.
// Only a regular file is removed.
func RemoveStaleIndexLock(dir string) (path string, age time.Duration, err error) {
	lock, fi := findIndexLock(dir)
	if lock == "" {
		return "", 0, nil
	}
	age = time.Since(fi.ModTime())
	if err := os.Remove(lock); err != nil {
		return lock, age, fmt.Errorf("remove %s: %w", lock, err)
	}
	return lock, age, nil
}

// HasIndexLock returns the path of the index.lock file present in the repository
// at dir, "" when there is none. It removes nothing and applies the guards of
// RemoveStaleIndexLock (dir is the root of its own repository).
func HasIndexLock(dir string) string {
	lock, _ := findIndexLock(dir)
	return lock
}

// findIndexLock is the regular index.lock file of the repository rooted at dir,
// with its stat; "" when dir is not such a root or there is no such file.
func findIndexLock(dir string) (string, os.FileInfo) {
	if _, statErr := os.Lstat(filepath.Join(dir, ".git")); statErr != nil {
		return "", nil
	}
	if !isOwnToplevel(dir) {
		return "", nil
	}
	lock := indexLockPath(dir, nil)
	if lock == "" {
		return "", nil
	}
	fi, statErr := os.Lstat(lock)
	if statErr != nil || !fi.Mode().IsRegular() {
		return "", nil
	}
	return lock, fi
}

// isOwnToplevel reports whether the repository git finds at dir has dir as its
// top level, symlinks resolved on both sides (Windows and macOS report the same
// folder under different spellings, and Windows paths compare case-insensitively).
func isOwnToplevel(dir string) bool {
	out, err := runGit(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return false
	}
	top, err := filepath.EvalSymlinks(filepath.FromSlash(strings.TrimSpace(out)))
	if err != nil {
		return false
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false
	}
	top, want = filepath.Clean(top), filepath.Clean(want)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(top, want)
	}
	return top == want
}
