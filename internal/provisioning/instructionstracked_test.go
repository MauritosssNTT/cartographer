package provisioning

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
	"github.com/BeppeTemp/cartographer/internal/safewrite"
)

// #1897: the "Local paths" table holds absolute paths of this machine, so a
// project-scope block that lands in a file git tracks carries the paragraph and
// no rows. The table is the part of the block that differs per developer.

const (
	machinePath   = "/srv/design-machine"
	tableHeader   = "| Placeholder | Local path |"
	paragraphHead = "### Local paths"
)

// trackedFixture is a repository whose AGENTS.md is tracked, plus the
// manifest and options a project-scope client sync would build for it. The
// placeholder is cited by the KB's concepts only (Placeholders), so the machine
// path can reach the block through the table and nowhere else.
func trackedFixture(t *testing.T, provider configurator.Provider) (dir string, m contract.Manifest, opts ApplyOptions) {
	t.Helper()
	dir = t.TempDir()
	git(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# Project rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "AGENTS.md")
	a := instructionsArtifactForExpandTest("kb-a", "Plain instructions.\n")
	m = contract.MergeArtifacts([]contract.Artifact{a})
	opts = ApplyOptions{
		ApprovedMCP:        approveAll(m),
		Provider:           provider,
		BaseDir:            dir,
		Scope:              contract.ScopeProject,
		ExpandPlaceholders: true,
		Paths:              map[string]string{"design": machinePath},
		Placeholders:       map[string][]string{"path:design": {"kb-a"}},
		SkipLockWrite:      true,
	}
	return dir, m, opts
}

// applyThenJudge is one sync followed by the status judgement of the lock it
// recorded, both with the flag the client derives for the projection (the same
// answer on both sides), then a second sync that must write nothing.
func applyThenJudge(t *testing.T, m contract.Manifest, opts ApplyOptions) (block string) {
	t.Helper()
	opts.OmitPathsTable = InstructionsDestTracked(opts)
	first, err := Apply(m, opts)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(first.Written) == 0 {
		t.Fatalf("the first sync must write the block: %+v", first)
	}
	opts.Lock = first.NewLock
	if d := ComputeDiffResolved(m, first.NewLock, opts); !d.InSync {
		t.Errorf("status after the sync must be in sync, not drift forever: %+v", d)
	}
	second, err := Apply(m, opts)
	if err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	if len(second.Written) != 0 {
		t.Errorf("the second sync must write nothing: %+v", second.Written)
	}
	data, err := os.ReadFile(filepath.Join(opts.BaseDir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestApply_TrackedInstructionsFileOmitsThePathsTable(t *testing.T) {
	_, m, opts := trackedFixture(t, configurator.ProviderCodex)
	block := applyThenJudge(t, m, opts)
	if !strings.Contains(block, paragraphHead) || !strings.Contains(block, "cartographer resolve") {
		t.Errorf("the tracked file keeps the paragraph that points to `cartographer resolve`:\n%s", block)
	}
	if strings.Contains(block, tableHeader) || strings.Contains(block, machinePath) {
		t.Errorf("a machine path reached a tracked file:\n%s", block)
	}
	if !strings.Contains(block, "# Project rules") {
		t.Errorf("the repository's own text must survive:\n%s", block)
	}
}

func TestApply_UntrackedInstructionsFileKeepsThePathsTable(t *testing.T) {
	dir, m, opts := trackedFixture(t, configurator.ProviderCodex)
	git(t, dir, "rm", "-q", "--cached", "AGENTS.md")
	block := applyThenJudge(t, m, opts)
	if !strings.Contains(block, tableHeader) || !strings.Contains(block, machinePath) {
		t.Errorf("an untracked file keeps the table:\n%s", block)
	}
}

// An instructions file the client owns (InstructionsRel, D1019) keeps the table
// even when the destination the matrix names is tracked: it is the file written.
func TestApply_OwnedInstructionsFileKeepsThePathsTable(t *testing.T) {
	dir, m, opts := trackedFixture(t, configurator.ProviderClaudeCode)
	opts.InstructionsRel = "CLAUDE.local.md"
	if InstructionsDestTracked(opts) {
		t.Fatal("CLAUDE.local.md is not tracked")
	}
	opts.OmitPathsTable = false
	if _, err := Apply(m, opts); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "CLAUDE.local.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), tableHeader) {
		t.Errorf("the owned file keeps the table:\n%s", data)
	}
}

// CLAUDE.md -> AGENTS.md: the block lands in the tracked target through the link
// the operator listed in allow_symlinks, so the link counts as tracked.
func TestApply_SymlinkToTrackedFileOmitsThePathsTable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	dir, m, opts := trackedFixture(t, configurator.ProviderClaudeCode)
	link := filepath.Join(dir, "CLAUDE.md")
	if err := os.Symlink("AGENTS.md", link); err != nil {
		t.Fatal(err)
	}
	safewrite.SetAllowedLinks([]string{link})
	t.Cleanup(safewrite.ResetAllowedLinks)
	if !InstructionsDestTracked(opts) {
		t.Fatal("a link to a tracked file inside the workspace is a tracked destination")
	}
	opts.OmitPathsTable = true
	first, err := Apply(m, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Written) == 0 {
		t.Fatalf("nothing written through the link: %+v", first)
	}
	data, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), paragraphHead) || strings.Contains(string(data), tableHeader) || strings.Contains(string(data), machinePath) {
		t.Errorf("the tracked target must hold the paragraph and no machine path:\n%s", data)
	}
	opts.Lock = first.NewLock
	if d := ComputeDiffResolved(m, first.NewLock, opts); !d.InSync {
		t.Errorf("status after the sync must be in sync: %+v", d)
	}
}

// Status and sync judge one hash: a status that forgot the flag would see a
// table the block no longer has and report drift for good (TestShared_Convergence).
func TestComputeDiffResolved_TrackedBlockNeedsTheSameFlag(t *testing.T) {
	_, m, opts := trackedFixture(t, configurator.ProviderCodex)
	opts.OmitPathsTable = true
	first, err := Apply(m, opts)
	if err != nil {
		t.Fatal(err)
	}
	opts.OmitPathsTable = false
	if d := ComputeDiffResolved(m, first.NewLock, opts); d.InSync {
		t.Errorf("a status that keeps the table must not read a table-less block as in sync: %+v", d)
	}
	opts.OmitPathsTable = true
	if d := ComputeDiffResolved(m, first.NewLock, opts); !d.InSync {
		t.Errorf("a status with the sync's flag is in sync: %+v", d)
	}
}

// The file is committed after a first sync wrote the table: the next sync takes
// the rows out, with no artifact change, through the paths-section hash.
func TestApply_TrackingTheFileLaterRemovesTheTable(t *testing.T) {
	dir, m, opts := trackedFixture(t, configurator.ProviderCodex)
	git(t, dir, "rm", "-q", "--cached", "AGENTS.md")
	opts.OmitPathsTable = InstructionsDestTracked(opts)
	first, err := Apply(m, opts)
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md")); !strings.Contains(string(data), tableHeader) {
		t.Fatalf("untracked: the table is expected:\n%s", data)
	}
	git(t, dir, "add", "AGENTS.md")
	opts.Lock = first.NewLock
	opts.OmitPathsTable = InstructionsDestTracked(opts)
	if !opts.OmitPathsTable {
		t.Fatal("AGENTS.md is tracked now")
	}
	if d := ComputeDiffResolved(m, first.NewLock, opts); d.InSync {
		t.Errorf("tracking the file is a pending change: %+v", d)
	}
	second, err := Apply(m, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Written) == 0 {
		t.Fatalf("the block must be rewritten without the table: %+v", second)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md")); strings.Contains(string(data), tableHeader) {
		t.Errorf("the table is still there:\n%s", data)
	}
}

func TestInstructionsDestTracked_Answers(t *testing.T) {
	dir, _, opts := trackedFixture(t, configurator.ProviderCodex)
	if !InstructionsDestTracked(opts) {
		t.Error("a tracked AGENTS.md is a tracked destination")
	}
	global := opts
	global.Scope = contract.ScopeGlobal
	if InstructionsDestTracked(global) {
		t.Error("the global scope is never judged")
	}
	elsewhere := opts
	elsewhere.BaseDir = t.TempDir() // in no repository
	if InstructionsDestTracked(elsewhere) {
		t.Error("a directory in no repository tracks nothing")
	}
	owned := opts
	owned.InstructionsRel = "AGENTS.local.md"
	if InstructionsDestTracked(owned) {
		t.Error("an untracked InstructionsRel is not tracked, whatever the matrix names")
	}
	// A repository git cannot read keeps today's behaviour: the table stays.
	prev := gitTrackedFn
	gitTrackedFn = func(string) (map[string]bool, error) { return nil, errors.New("dubious ownership") }
	t.Cleanup(func() { gitTrackedFn = prev })
	if InstructionsDestTracked(opts) {
		t.Errorf("a TrackedPaths error must not omit the table (%s)", dir)
	}
}

// A link that leaves the workspace says nothing about its tracked set.
func TestInstructionsDestTracked_LinkOutsideTheWorkspace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	dir, _, opts := trackedFixture(t, configurator.ProviderClaudeCode)
	outside := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := os.WriteFile(outside, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
	if InstructionsDestTracked(opts) {
		t.Error("a target outside the workspace is not in its tracked set")
	}
}
