package gitx

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// TestWorkTreeIdentFollowsTheRepository: the identity is the one git applies
// in that repository, an includeIf gitdir: one included; outside any work
// tree there is none, though a global identity exists, and GIT_DIR in the
// environment does not move the question to another repository.
func TestWorkTreeIdentFollowsTheRepository(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH, skipping gitx tests")
	}
	home := isolateGitIdentity(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", root)
	work, personal, outside := filepath.Join(root, "work"), filepath.Join(root, "personal"), filepath.Join(root, "outside")
	for _, d := range []string{work, personal, filepath.Join(work, "docs"), outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []string{work, personal} {
		if err := Init(d); err != nil {
			t.Fatal(err)
		}
	}
	// The pattern is built from the path git reports, so it matches on every
	// platform the way git compares it.
	gitDir, err := runGit(work, "rev-parse", "--absolute-git-dir")
	if err != nil {
		t.Fatal(err)
	}
	workInclude := filepath.Join(home, "work.gitconfig")
	global := "[user]\n\tname = Home Person\n\temail = home@example.test\n" +
		"[includeIf \"gitdir:" + path.Dir(strings.TrimSpace(gitDir)) + "/\"]\n\tpath = " + filepath.ToSlash(workInclude) + "\n"
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(global), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workInclude, []byte("[user]\n\tname = Work Person\n\temail = work@example.test\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for dir, want := range map[string]string{
		work:                          "Work Person <work@example.test>",
		filepath.Join(work, "docs"):   "Work Person <work@example.test>",
		personal:                      "Home Person <home@example.test>",
		outside:                       "",
		filepath.Join(work, ".git"):   "",
		filepath.Join(root, "absent"): "",
	} {
		if got := WorkTreeIdent(dir); got != want {
			t.Errorf("WorkTreeIdent(%s) = %q, want %q", dir, got, want)
		}
	}

	t.Setenv("GIT_DIR", filepath.Join(personal, ".git"))
	if got := WorkTreeIdent(work); got != "Work Person <work@example.test>" {
		t.Errorf("with GIT_DIR naming another repository, WorkTreeIdent(work) = %q", got)
	}
}

// TestWorkTreeIdentNeverGuesses: a name with no email configured gives no
// identity, not the user@host git would otherwise make up or the EMAIL
// variable; the GIT_AUTHOR_* variables still apply, as they do to a commit.
func TestWorkTreeIdentNeverGuesses(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH, skipping gitx tests")
	}
	home := isolateGitIdentity(t)
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[user]\n\tname = Name Only\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	if err := Init(repo); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EMAIL", "guess@example.test")
	if got := WorkTreeIdent(repo); got != "" {
		t.Errorf("WorkTreeIdent with no user.email = %q, want none", got)
	}
	t.Setenv("GIT_AUTHOR_EMAIL", "env@example.test")
	if got := WorkTreeIdent(repo); got != "Name Only <env@example.test>" {
		t.Errorf("WorkTreeIdent with GIT_AUTHOR_EMAIL = %q, want it applied", got)
	}
}

func TestGlobalIdentNeverCarriesAPerFolderIdentity(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH, skipping gitx tests")
	}
	home := isolateGitIdentity(t)
	t.Setenv("GIT_CONFIG_GLOBAL", "")
	os.Unsetenv("GIT_CONFIG_GLOBAL")
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	slash := func(name string) string { return filepath.ToSlash(filepath.Join(home, name)) }
	const person = "[user]\n\tname = Home Person\n\temail = home@example.test\n"
	const homePerson = "Home Person <home@example.test>"
	included := slash("id.gitconfig")
	write(included, "[user]\n\tname = Included Person\n\temail = inc@example.test\n")
	// An included file that changes how git connects, never who it names.
	sshOnly := slash("ssh.gitconfig")
	write(sshOnly, "[core]\n\tsshCommand = ssh -i /key\n")
	authorOnly := slash("author.gitconfig")
	write(authorOnly, "[author]\n\temail = author@example.test\n")
	nestedCond := slash("nested-cond.gitconfig")
	write(nestedCond, "[includeIf \"gitdir:~/deeper/\"]\n\tpath = "+sshOnly+"\n")
	nestedUncond := slash("nested-uncond.gitconfig")
	write(nestedUncond, "[include]\n\tpath = "+included+"\n")
	cond := func(condition, path string) string {
		return "[includeIf \"" + condition + "\"]\n\tpath = " + path + "\n"
	}
	for _, tc := range []struct {
		name, global, xdg, want string
		perFolder               bool
	}{
		{"plain", person, "", homePerson, false},
		{"unconditional include", "[include]\n\tpath = " + included + "\n", "", "Included Person <inc@example.test>", false},
		{"includeIf", person + cond("gitdir:~/work/", included), "", "", true},
		{"includeIf with no user set", person + cond("gitdir:~/work/", sshOnly), "", homePerson, false},
		{"includeIf with no user set, ~ path", person + cond("gitdir:~/work/", "~/ssh.gitconfig"), "", homePerson, false},
		{"includeIf with no user set, relative path", person + cond("gitdir:~/work/", "ssh.gitconfig"), "", homePerson, false},
		{"includeIf with no user set, space in the condition", person + cond("gitdir:~/my work/", sshOnly), "", homePerson, false},
		{"includeIf with a user set, space in the condition", person + cond("gitdir:~/my work/", included), "", "", true},
		{"includeIf setting only author.email", person + cond("gitdir:~/work/", authorOnly), "", "", true},
		{"includeIf to a missing file", person + cond("gitdir:~/work/", slash("missing.gitconfig")), "", "", true},
		{"includeIf to a file that includes a user", person + cond("gitdir:~/work/", nestedUncond), "", "", true},
		{"includeIf to a file with an includeIf", person + cond("gitdir:~/work/", nestedCond), "", "", true},
		{"includeIf key that is not a path", person + "[includeIf \"gitdir:~/work/\"]\n\tsomething = else\n", "", "", true},
		{"one harmless and one identity", person + cond("gitdir:~/a/", sshOnly) + cond("gitdir:~/b/", included), "", "", true},
		{"includeIf in the XDG file, no user set", "", person + cond("gitdir:~/work/", sshOnly), homePerson, false},
		{"includeIf in the XDG file, a user set", "", person + cond("gitdir:~/work/", included), "", true},
		{"name only", "[user]\n\tname = Home Person\n", "", "", false},
		{"invalid", "[user]\n\tname = Home <Person>\n\temail = home@example.test\n", "", "", false},
		{"none", "", "", "", false},
	} {
		// git config --global reads ~/.gitconfig, or the XDG file when there is
		// none: a test of the XDG file leaves ~/.gitconfig out.
		gitconfig, xdg := filepath.Join(home, ".gitconfig"), filepath.Join(home, "git", "config")
		os.Remove(gitconfig)
		os.Remove(xdg)
		if tc.xdg != "" {
			write(xdg, tc.xdg)
		} else {
			write(gitconfig, tc.global)
		}
		t.Setenv("GIT_AUTHOR_NAME", "Process Person")
		t.Setenv("GIT_AUTHOR_EMAIL", "process@example.test")
		if got, perFolder := GlobalIdent(); got != tc.want || perFolder != tc.perFolder {
			t.Errorf("%s: GlobalIdent() = %q, %v, want %q, %v", tc.name, got, perFolder, tc.want, tc.perFolder)
		}
	}
}
