package gitx

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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

// RemoveStaleIndexLock removes the index.lock a crash, a power loss or a kill left
// in the repository at dir, and returns its path and age (path "" when there was
// none). The caller must hold the KB's exclusive process lock and run it before
// serving: only then no git of this server runs in the clone, so a lock found is
// a leftover. dir must be the root of its own repository or linked worktree (a
// `.git` directory or file in it), never a folder inside a larger one, whose lock
// is not ours to clear. The lock path comes from git itself (rev-parse --git-path),
// so a worktree or a `.git` file pointing elsewhere resolves to the right git dir.
// Only a regular file is removed.
func RemoveStaleIndexLock(dir string) (path string, age time.Duration, err error) {
	if _, statErr := os.Lstat(filepath.Join(dir, ".git")); statErr != nil {
		return "", 0, nil
	}
	lock := indexLockPath(dir, nil)
	if lock == "" {
		return "", 0, nil
	}
	fi, statErr := os.Lstat(lock)
	if statErr != nil || !fi.Mode().IsRegular() {
		return "", 0, nil
	}
	age = time.Since(fi.ModTime())
	if err := os.Remove(lock); err != nil {
		return lock, age, fmt.Errorf("remove %s: %w", lock, err)
	}
	return lock, age, nil
}
