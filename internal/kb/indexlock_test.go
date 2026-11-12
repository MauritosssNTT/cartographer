package kb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// leaveIndexLock plants the index.lock a killed git leaves, an hour old.
func leaveIndexLock(t *testing.T, k *KB) string {
	t.Helper()
	lock := filepath.Join(k.Root, ".git", "index.lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	return lock
}

func writeOne(t *testing.T, k *KB, rel string) error {
	t.Helper()
	if err := k.WriteFileAtomic(rel, []byte("x\n")); err != nil {
		t.Fatal(err)
	}
	_, err := k.CommitOpAs("write "+rel, "", "")
	return err
}

// A lock left by a crash is cleared when the server opens the KB, and the first
// write after it commits (#1997).
func TestClearStaleIndexLock_StaleLockAtOpenIsRemovedAndTheWriteSucceeds(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k := mustInitKB(t)
	lock := leaveIndexLock(t, k)

	k.ClearStaleIndexLock()

	if _, err := os.Lstat(lock); !os.IsNotExist(err) {
		t.Fatalf("the stale index.lock is still there: %v", err)
	}
	if err := writeOne(t, k, "data/a.md"); err != nil {
		t.Fatalf("the first write after the clean-up failed: %v", err)
	}
}

// A lock that appears after open may be an operator's own git: it is kept, and the
// write error names the file and says when to remove it.
func TestClearStaleIndexLock_LockAfterOpenIsKeptAndNamedInTheWriteError(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k := mustInitKB(t)
	k.ClearStaleIndexLock()
	lock := leaveIndexLock(t, k)

	err := writeOne(t, k, "data/a.md")
	if err == nil {
		t.Fatal("the write succeeded with an index.lock in the clone")
	}
	if _, statErr := os.Lstat(lock); statErr != nil {
		t.Fatalf("a lock that appeared after open was removed: %v", statErr)
	}
	for _, want := range []string{"index.lock", "if no git process runs in", "remove that file"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the write error lacks %q:\n%v", want, err)
		}
	}
	if !strings.Contains(filepath.ToSlash(err.Error()), filepath.ToSlash(lock)) {
		t.Errorf("the write error does not name %s:\n%v", lock, err)
	}
}

// Another process holding the KB's lock may have a git running in the clone: nothing
// is removed.
func TestClearStaleIndexLock_LeavesTheLockWhileAnotherHolderHasTheKB(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k := mustInitKB(t)
	lock := leaveIndexLock(t, k)
	release, err := k.AcquireProcessLock(time.Second, "other process")
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	k.ClearStaleIndexLock()

	if _, err := os.Lstat(lock); err != nil {
		t.Fatalf("the lock was removed while the KB was held elsewhere: %v", err)
	}
}
