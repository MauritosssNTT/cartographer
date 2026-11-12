package repodocs

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// pipeGrepQuiet matches `<writer> | grep -q…` (any flag cluster with q, or
// --quiet/--silent) and not `||`.
var pipeGrepQuiet = regexp.MustCompile(`(^|[^|])\|\s*e?f?grep\s+([^|;&]*\s)?(-[a-zA-Z]*q|--quiet|--silent)`)

// TestE2EScriptsNoPipeIntoGrepQuiet: the e2e scenarios run under
// `set -o pipefail`, and `x | grep -q` exits on the first match while x may
// still be writing, so x dies of SIGPIPE and a passing check reads as a miss
// (and a not-discloses check passes on a leak: #1908). The helpers and the
// scenarios use a here-string, `grep -q … <<<"$x"`. test/e2e/lib/selftest.sh
// pins the helpers with a body larger than a pipe; this test keeps the
// pattern out of every script there. A line that shows the trap on purpose
// carries `e2e-pipe-ok`.
func TestE2EScriptsNoPipeIntoGrepQuiet(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "test", "e2e")
	n := 0
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".sh") {
			return nil
		}
		n++
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		for i, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") || strings.Contains(line, "e2e-pipe-ok") {
				continue
			}
			if pipeGrepQuiet.MatchString(line) {
				t.Errorf("%s:%d: a pipe into grep -q fails a passing check under pipefail (SIGPIPE); use a here-string: grep -q … <<<\"$x\"\n  %s", filepath.ToSlash(rel), i+1, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("no .sh file under test/e2e: the guard reads nothing")
	}
}
