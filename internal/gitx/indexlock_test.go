package gitx

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repoWithCommit(t *testing.T) string {
	t.Helper()
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	dir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Commit(dir, "first", "User", "user@example.com"); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRemoveStaleIndexLock_RemovesTheLeftoverAndReportsItsAge(t *testing.T) {
	dir := repoWithCommit(t)
	lock := filepath.Join(dir, ".git", "index.lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	path, age, err := RemoveStaleIndexLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(path) != filepath.Clean(lock) || age < 0 {
		t.Fatalf("removed %q (age %s), want %q", path, age, lock)
	}
	if _, err := os.Lstat(lock); !os.IsNotExist(err) {
		t.Fatalf("the lock is still there: %v", err)
	}
	if path, _, err := RemoveStaleIndexLock(dir); path != "" || err != nil {
		t.Fatalf("with no lock: %q, %v", path, err)
	}
}

// A linked worktree has a `.git` file: the lock to clear is in the git dir it names.
func TestRemoveStaleIndexLock_ResolvesALinkedWorktreesGitDir(t *testing.T) {
	main := repoWithCommit(t)
	wt := filepath.Join(t.TempDir(), "wt")
	if out, err := runGit(main, "worktree", "add", "-q", "-b", "side", wt); err != nil {
		t.Fatalf("worktree add: %v\n%s", err, out)
	}
	if fi, err := os.Lstat(filepath.Join(wt, ".git")); err != nil || fi.IsDir() {
		t.Fatalf("the worktree's .git should be a file: %v", err)
	}
	out, err := runGit(wt, "rev-parse", "--git-path", "index.lock")
	if err != nil {
		t.Fatal(err)
	}
	lock := strings.TrimSpace(out)
	if !filepath.IsAbs(lock) {
		lock = filepath.Join(wt, lock)
	}
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	mainLock := filepath.Join(main, ".git", "index.lock")
	if err := os.WriteFile(mainLock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	path, _, err := RemoveStaleIndexLock(wt)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(path) != filepath.Clean(lock) {
		t.Fatalf("removed %q, want the worktree's %q", path, lock)
	}
	if _, err := os.Lstat(lock); !os.IsNotExist(err) {
		t.Fatalf("the worktree's lock is still there: %v", err)
	}
	if _, err := os.Lstat(mainLock); err != nil {
		t.Fatalf("the main clone's lock was touched: %v", err)
	}
}

// A folder inside a larger repository is not a repository of its own: the lock of the
// enclosing one is not ours to clear.
func TestRemoveStaleIndexLock_LeavesTheLockOfAnEnclosingRepository(t *testing.T) {
	outer := repoWithCommit(t)
	inner := filepath.Join(outer, "kb")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(outer, ".git", "index.lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if path, _, err := RemoveStaleIndexLock(inner); path != "" || err != nil {
		t.Fatalf("cleared %q (%v) from inside a larger repository", path, err)
	}
	if _, err := os.Lstat(lock); err != nil {
		t.Fatalf("the enclosing repository's lock was removed: %v", err)
	}
}

// Each staging command that git refuses on the lock says which file and when to remove it.
func TestIndexLockHint_NamedOnTheStagingCommands(t *testing.T) {
	dir := repoWithCommit(t)
	lock := filepath.Join(dir, ".git", "index.lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.md"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	errs := map[string]error{
		"Commit":     Commit(dir, "second", "User", "user@example.com"),
		"AddPath":    AddPath(dir, "b.md"),
		"Untrack":    Untrack(dir, "a.md"),
		"ResetPaths": ResetPaths(dir, "a.md"),
	}
	for name, err := range errs {
		if err == nil {
			t.Errorf("%s succeeded with an index.lock", name)
			continue
		}
		msg := filepath.ToSlash(err.Error())
		if !strings.Contains(msg, filepath.ToSlash(lock)) || !strings.Contains(msg, "if no git process runs in") {
			t.Errorf("%s: the error does not name the lock and the remedy:\n%v", name, err)
		}
	}
}

func TestIndexLockHint_IgnoresOtherFailures(t *testing.T) {
	if h := indexLockHint("d", "fatal: pathspec 'x' did not match any files\n"); h != "" {
		t.Fatalf("hint for an unrelated failure: %q", h)
	}
}

// The other index-touching wrappers say the same: which file, and when to remove it.
func TestIndexLockHint_NamedOnTheOtherIndexWrappers(t *testing.T) {
	dir := repoWithCommit(t)
	ident := []string{"GIT_AUTHOR_NAME=User", "GIT_AUTHOR_EMAIL=user@example.com", "GIT_COMMITTER_NAME=User", "GIT_COMMITTER_EMAIL=user@example.com"}
	git := func(args ...string) {
		t.Helper()
		if out, err := runGitEnv(dir, ident, args...); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	// A side branch one commit ahead (to merge), a commit to revert, a stash to pop.
	git("checkout", "-q", "-b", "side")
	if err := os.WriteFile(filepath.Join(dir, "side.md"), []byte("s\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "side.md")
	git("commit", "-q", "-m", "side")
	sideSHA, err := HeadSHA(dir)
	if err != nil {
		t.Fatal(err)
	}
	git("checkout", "-q", "-")
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("a changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("stash", "push", "-q")
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("a again\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.md"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	lock := filepath.Join(dir, ".git", "index.lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, mergeErr := MergeNoCommitNoFFEnv(dir, "side", ident...)
	errs := map[string]error{
		"StageExecutable":      StageExecutable(dir, "b.md"),
		"RevertNoCommit":       RevertNoCommit(dir, sideSHA, ident...),
		"ResetHardTo":          ResetHardTo(dir, "HEAD", ident...),
		"DiscardChanges":       DiscardChanges(dir),
		"MergeNoCommitNoFFEnv": mergeErr,
		"StashPushEnv":         StashPushEnv(dir, ident...),
		"StashPushAllEnv":      StashPushAllEnv(dir, ident...),
		"StashPop":             StashPop(dir),
	}
	for name, err := range errs {
		if err == nil {
			t.Errorf("%s succeeded with an index.lock", name)
			continue
		}
		msg := filepath.ToSlash(err.Error())
		if !strings.Contains(msg, filepath.ToSlash(lock)) || !strings.Contains(msg, "if no git process runs in") {
			t.Errorf("%s: the error does not name the lock and the remedy:\n%v", name, err)
		}
	}
}

// remoteAhead returns a clone of a bare remote one local commit ahead of a fetched
// upstream that moved on by one commit: the rebase has real work to do.
func remoteAhead(t *testing.T) (clone string) {
	t.Helper()
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	base := t.TempDir()
	bare := filepath.Join(base, "remote.git")
	ident := []string{"GIT_AUTHOR_NAME=User", "GIT_AUTHOR_EMAIL=user@example.com", "GIT_COMMITTER_NAME=User", "GIT_COMMITTER_EMAIL=user@example.com"}
	git := func(dir string, args ...string) {
		t.Helper()
		if out, err := runGitEnv(dir, ident, args...); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git(base, "init", "-q", "--bare", "-b", "main", bare)
	seed := filepath.Join(base, "seed")
	clone = filepath.Join(base, "clone")
	git(base, "clone", "-q", bare, seed)
	git(seed, "checkout", "-q", "-B", "main")
	put := func(dir, name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		git(dir, "add", name)
		git(dir, "commit", "-q", "-m", name)
	}
	put(seed, "seed.md")
	git(seed, "push", "-q", "origin", "main")
	git(base, "clone", "-q", bare, clone)
	put(clone, "local.md")
	put(seed, "upstream.md")
	git(seed, "push", "-q", "origin", "main")
	git(clone, "fetch", "-q", "origin")
	return clone
}

// git words a refused lock "error: could not detach HEAD" and the rebase wrappers
// took that for a conflict with no files: they name the lock instead, and leave the
// local commit where it was.
func TestIndexLockHint_NamedOnTheRebaseWrappers(t *testing.T) {
	for name, run := range map[string]func(dir string) error{
		"RebaseOntoAutostash": func(dir string) error { return RebaseOntoAutostash(dir, "origin", "main") },
		"PullRebaseAutostash": func(dir string) error { return PullRebaseAutostash(dir, "origin", "main") },
	} {
		t.Run(name, func(t *testing.T) {
			dir := remoteAhead(t)
			before, _ := HeadSHA(dir)
			lock := filepath.Join(dir, ".git", "index.lock")
			if err := os.WriteFile(lock, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			err := run(dir)
			if err == nil {
				t.Fatal("the rebase succeeded with an index.lock")
			}
			var rce *RebaseConflictError
			if errors.As(err, &rce) || errors.Is(err, ErrRebaseConflict) {
				t.Fatalf("a leftover lock was reported as a conflict: %v", err)
			}
			msg := filepath.ToSlash(err.Error())
			if !strings.Contains(msg, filepath.ToSlash(lock)) || !strings.Contains(msg, "if no git process runs in") {
				t.Errorf("the error does not name the lock and the remedy:\n%v", err)
			}
			if _, err := os.Lstat(lock); err != nil {
				t.Errorf("the lock was removed: %v", err)
			}
			if after, _ := HeadSHA(dir); after != before {
				t.Errorf("HEAD moved from %s to %s", before, after)
			}
			if _, ok := RebaseInProgress(dir); ok {
				t.Error("a rebase state was left behind")
			}
		})
	}
}

// A `.git` that is not a repository (empty) lets git walk up to the enclosing one:
// that repository's lock is not ours to clear.
func TestRemoveStaleIndexLock_LeavesTheLockWhenDotGitIsBogus(t *testing.T) {
	outer := repoWithCommit(t)
	inner := filepath.Join(outer, "kb")
	if err := os.MkdirAll(filepath.Join(inner, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(outer, ".git", "index.lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if path, _, err := RemoveStaleIndexLock(inner); path != "" || err != nil {
		t.Fatalf("cleared %q (%v) through an empty .git", path, err)
	}
	if _, err := os.Lstat(lock); err != nil {
		t.Fatalf("the enclosing repository's lock was removed: %v", err)
	}
}
