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
	opts.WithholdLocalPaths = InstructionsDestTracked(opts)
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
	opts.WithholdLocalPaths = false
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
	opts.WithholdLocalPaths = true
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
	opts.WithholdLocalPaths = true
	first, err := Apply(m, opts)
	if err != nil {
		t.Fatal(err)
	}
	opts.WithholdLocalPaths = false
	if d := ComputeDiffResolved(m, first.NewLock, opts); d.InSync {
		t.Errorf("a status that keeps the table must not read a table-less block as in sync: %+v", d)
	}
	opts.WithholdLocalPaths = true
	if d := ComputeDiffResolved(m, first.NewLock, opts); !d.InSync {
		t.Errorf("a status with the sync's flag is in sync: %+v", d)
	}
}

// The file is committed after a first sync wrote the table: the next sync takes
// the rows out, with no artifact change, through the paths-section hash.
func TestApply_TrackingTheFileLaterRemovesTheTable(t *testing.T) {
	dir, m, opts := trackedFixture(t, configurator.ProviderCodex)
	git(t, dir, "rm", "-q", "--cached", "AGENTS.md")
	opts.WithholdLocalPaths = InstructionsDestTracked(opts)
	first, err := Apply(m, opts)
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md")); !strings.Contains(string(data), tableHeader) {
		t.Fatalf("untracked: the table is expected:\n%s", data)
	}
	git(t, dir, "add", "AGENTS.md")
	opts.Lock = first.NewLock
	opts.WithholdLocalPaths = InstructionsDestTracked(opts)
	if !opts.WithholdLocalPaths {
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
	prev := trackedPathsFn
	trackedPathsFn = func(string, ...string) (map[string]bool, error) { return nil, errors.New("dubious ownership") }
	t.Cleanup(func() { trackedPathsFn = prev })
	if InstructionsDestTracked(opts) {
		t.Errorf("a TrackedPaths error must not omit the table (%s)", dir)
	}
}

// A destination that resolves outside the workspace fails closed: the
// workspace's tracked set says nothing about the file it lands in, so the paths
// are withheld, and no git command is run.
func TestInstructionsDestTracked_LinkOutsideTheWorkspaceWithholds(t *testing.T) {
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
	prev := trackedPathsFn
	trackedPathsFn = func(string, ...string) (map[string]bool, error) {
		t.Error("git must not be asked about a destination outside the workspace")
		return nil, errors.New("not asked")
	}
	t.Cleanup(func() { trackedPathsFn = prev })
	if !InstructionsDestTracked(opts) {
		t.Error("a target outside the workspace withholds the paths")
	}
}

// A workspace that is a subdirectory of a repository, with CLAUDE.md -> ../AGENTS.md
// (the tracked file one level up): the target is outside the workspace.
func TestInstructionsDestTracked_SubdirectoryWorkspaceLinkToParent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	root, _, opts := trackedFixture(t, configurator.ProviderClaudeCode)
	sub := filepath.Join(root, "service")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "AGENTS.md"), filepath.Join(sub, "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
	opts.BaseDir = sub
	if !InstructionsDestTracked(opts) {
		t.Error("CLAUDE.md -> ../AGENTS.md lands in the repository's tracked file: paths withheld")
	}
}

// A link in a directory component: .claude -> docs/agent makes
// .claude/CLAUDE.md the tracked docs/agent/CLAUDE.md. Only the leaf was followed.
func TestInstructionsDestTracked_SymlinkedDirectoryComponent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	if err := os.MkdirAll(filepath.Join(dir, "docs", "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs", "agent", "CLAUDE.md"), []byte("# rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", filepath.Join("docs", "agent", "CLAUDE.md"))
	if err := os.Symlink(filepath.Join("docs", "agent"), filepath.Join(dir, ".claude")); err != nil {
		t.Fatal(err)
	}
	opts := ApplyOptions{Provider: configurator.ProviderClaudeCode, BaseDir: dir, Scope: contract.ScopeProject, InstructionsRel: ".claude/CLAUDE.md"}
	if !InstructionsDestTracked(opts) {
		t.Error("a destination behind a symlinked directory resolves to the tracked file")
	}
	git(t, dir, "rm", "-q", "--cached", filepath.Join("docs", "agent", "CLAUDE.md"))
	if InstructionsDestTracked(opts) {
		t.Error("untracked once the resolved file is no longer in the index")
	}
}

// The question is asked about the one or two paths concerned, never for the
// whole index.
func TestInstructionsDestTracked_AsksAboutTheDestinationOnly(t *testing.T) {
	_, _, opts := trackedFixture(t, configurator.ProviderCodex)
	prev := trackedPathsFn
	var asked []string
	trackedPathsFn = func(_ string, paths ...string) (map[string]bool, error) {
		asked = append(asked, paths...)
		return map[string]bool{}, nil
	}
	t.Cleanup(func() { trackedPathsFn = prev })
	if InstructionsDestTracked(opts) {
		t.Error("the stub tracks nothing")
	}
	if len(asked) != 1 || asked[0] != "AGENTS.md" {
		t.Errorf("git was asked about %v, want just AGENTS.md", asked)
	}
}

// --- the KB's own instruction text (#1897) ---

const citedText = "Design docs: {{path:design}}. Written down: {{\\path:design}}. Syntax: {{repo:<key>}}.\n"

// instructionsFixture is trackedFixture with a KB whose instruction text cites
// a placeholder itself, plus, with skill, a skill that cites the same key.
func instructionsFixture(t *testing.T, provider configurator.Provider, skill bool) (dir string, m contract.Manifest, opts ApplyOptions) {
	t.Helper()
	dir, _, opts = trackedFixture(t, provider)
	if !skill {
		m = contract.MergeArtifacts([]contract.Artifact{instructionsArtifactForExpandTest("kb-a", citedText)})
		opts.ApprovedMCP = approveAll(m)
		return dir, m, opts
	}
	kbRoot := t.TempDir()
	skillDir := filepath.Join(kbRoot, "skills", "kb-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: kb-skill\ndescription: Test\n---\nSee {{path:design}}.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sm, err := contract.BuildManifest(nil, map[string]string{"kb-a": kbRoot}, contract.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	arts := append([]contract.Artifact{instructionsArtifactForExpandTest("kb-a", citedText)}, sm.Artifacts...)
	m = contract.MergeArtifacts(arts)
	opts.ApprovedMCP = approveAll(m)
	opts.KBRoots = map[string]string{"kb-a": kbRoot}
	return dir, m, opts
}

func TestApply_TrackedInstructionsFileKeepsTheKBsPlaceholdersUnexpanded(t *testing.T) {
	_, m, opts := instructionsFixture(t, configurator.ProviderCodex, false)
	block := applyThenJudge(t, m, opts)
	if strings.Contains(block, machinePath) {
		t.Errorf("the KB's own placeholder leaked a machine path into a tracked file:\n%s", block)
	}
	for _, want := range []string{
		"Design docs: {{path:design}}.",  // left as written for the agent to resolve
		"Written down: {{path:design}}.", // the escape is still removed
		"Syntax: {{repo:<key>}}.",        // metasyntax stays verbatim
		paragraphHead,
	} {
		if !strings.Contains(block, want) {
			t.Errorf("block lacks %q:\n%s", want, block)
		}
	}
}

// A skill is written to its own file, not into the tracked block: it keeps its
// expansion while the block withholds the same key.
func TestApply_TrackedInstructionsFileLeavesSkillExpansionAlone(t *testing.T) {
	dir, m, opts := instructionsFixture(t, configurator.ProviderCodex, true)
	opts.WithholdLocalPaths = InstructionsDestTracked(opts)
	if !opts.WithholdLocalPaths {
		t.Fatal("AGENTS.md is tracked")
	}
	if _, err := Apply(m, opts); err != nil {
		t.Fatal(err)
	}
	skill, err := os.ReadFile(filepath.Join(dir, ".agents", "skills", "kb-skill", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(skill), machinePath) {
		t.Errorf("the skill keeps its expansion:\n%s", skill)
	}
	block, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(block), machinePath) {
		t.Errorf("the block must not carry the machine path:\n%s", block)
	}
}

func TestApply_UntrackedInstructionsFileExpandsTheKBsPlaceholders(t *testing.T) {
	dir, m, opts := instructionsFixture(t, configurator.ProviderCodex, false)
	git(t, dir, "rm", "-q", "--cached", "AGENTS.md")
	block := applyThenJudge(t, m, opts)
	if !strings.Contains(block, "Design docs: "+machinePath+".") || !strings.Contains(block, "Written down: {{path:design}}.") {
		t.Errorf("an untracked file keeps today's expansion:\n%s", block)
	}
}

// Status judges the block with the sync's flag: the instruction text's own
// placeholders are part of what the paths-section hash covers only through the
// flag, and a status without it would report drift for good.
func TestComputeDiffResolved_KBPlaceholdersNeedTheSameFlag(t *testing.T) {
	_, m, opts := instructionsFixture(t, configurator.ProviderCodex, false)
	opts.WithholdLocalPaths = true
	first, err := Apply(m, opts)
	if err != nil {
		t.Fatal(err)
	}
	if d := ComputeDiffResolved(m, first.NewLock, opts); !d.InSync {
		t.Errorf("a status with the sync's flag is in sync: %+v", d)
	}
	opts.WithholdLocalPaths = false
	if d := ComputeDiffResolved(m, first.NewLock, opts); d.InSync {
		t.Errorf("a status without the flag must not read the withheld block as in sync: %+v", d)
	}
}

// --- the git environment (#1897) ---

// A sync run from a git hook inherits GIT_DIR/GIT_INDEX_FILE: the listing must
// answer for the workspace it is given, not for the repository running the hook.
func TestGitTrackedPaths_IgnoresInheritedRepoPins(t *testing.T) {
	ws := t.TempDir()
	git(t, ws, "init", "-q")
	if err := os.WriteFile(filepath.Join(ws, "AGENTS.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, ws, "add", "AGENTS.md")
	other := t.TempDir()
	git(t, other, "init", "-q")
	if err := os.WriteFile(filepath.Join(other, "OTHER.md"), []byte("y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, other, "add", "OTHER.md")
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_INDEX_FILE", filepath.Join(other, ".git", "index"))

	tracked, err := gitTrackedPaths(ws)
	if err != nil {
		t.Fatal(err)
	}
	if !tracked["AGENTS.md"] || tracked["OTHER.md"] {
		t.Errorf("tracked = %v, want the workspace's AGENTS.md and not the other repository's OTHER.md", tracked)
	}
	if ok, err := gitTracksFile(ws, "AGENTS.md"); err != nil || !ok {
		t.Errorf("gitTracksFile(AGENTS.md) = %v, %v", ok, err)
	}
	if ok, err := gitTracksFile(ws, "OTHER.md"); err != nil || ok {
		t.Errorf("gitTracksFile(OTHER.md) = %v, %v: it belongs to the other repository", ok, err)
	}
}
