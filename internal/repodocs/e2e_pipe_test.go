package repodocs

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// pipeMarker marks the one line that shows the trap on purpose. It is honoured
// only in pipeMarkerFile, so a scenario cannot wave the guard away.
const (
	pipeMarker     = "e2e-pipe-ok"
	pipeMarkerFile = "test/e2e/lib/selftest.sh"
)

var (
	quietFlagCluster = regexp.MustCompile(`^-[A-Za-z]*q[A-Za-z]*$`)
	pipefailSet      = regexp.MustCompile(`(?m)^\s*set\s[^#\n]*\bpipefail\b`)
)

// shellCommand is one simple command of a logical line: its words (quotes
// removed) and whether it is a later stage of a pipeline (`a | THIS`).
type shellCommand struct {
	words []string
	piped bool
}

// shellCommands splits one logical line into its commands. It is a scanner,
// not a shell parser: quotes are opaque (a `|` or `;` inside them separates
// nothing), `;`, `&`, `&&`, `||`, parentheses and backticks end a command, a
// single `|` ends it and marks the next one as piped, and an unquoted `#` at the
// start of a word ends the line. That is enough to tell `a | grep "x|y" -q`
// from `a || grep -q x`.
func shellCommands(line string) []shellCommand {
	var (
		cmds    []shellCommand
		cur     shellCommand
		word    strings.Builder
		inWord  bool
		single  bool
		double  bool
		rs      = []rune(line)
		endWord = func() {
			if inWord {
				cur.words = append(cur.words, word.String())
				word.Reset()
				inWord = false
			}
		}
		endCmd = func(nextPiped bool) {
			endWord()
			if len(cur.words) > 0 {
				cmds = append(cmds, cur)
			}
			cur = shellCommand{piped: nextPiped}
		}
	)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case single:
			if c == '\'' {
				single = false
			} else {
				word.WriteRune(c)
			}
		case double:
			switch {
			case c == '"':
				double = false
			case c == '\\' && i+1 < len(rs):
				i++
				word.WriteRune(rs[i])
			default:
				word.WriteRune(c)
			}
		case c == '\'':
			single, inWord = true, true
		case c == '"':
			double, inWord = true, true
		case c == '\\' && i+1 < len(rs):
			i++
			word.WriteRune(rs[i])
			inWord = true
		case c == '#' && !inWord:
			endCmd(false)
			return cmds
		case c == ' ' || c == '\t':
			endWord()
		case c == '|':
			if i+1 < len(rs) && rs[i+1] == '|' {
				i++
				endCmd(false)
			} else {
				endCmd(true)
			}
		case c == ';' || c == '&' || c == '(' || c == ')' || c == '`':
			endCmd(false)
		default:
			word.WriteRune(c)
			inWord = true
		}
	}
	endCmd(false)
	return cmds
}

// isQuietGrep: `[command|builtin|exec] [path/]grep|egrep|fgrep` with a quiet
// flag in any position (-q, a cluster such as -Eq or -qi, --quiet, --silent)
// before a `--`.
func isQuietGrep(words []string) bool {
	for len(words) > 0 && (words[0] == "command" || words[0] == "builtin" || words[0] == "exec") {
		words = words[1:]
	}
	if len(words) == 0 {
		return false
	}
	switch path.Base(words[0]) {
	case "grep", "egrep", "fgrep":
	default:
		return false
	}
	for _, w := range words[1:] {
		if w == "--" {
			return false
		}
		if w == "--quiet" || w == "--silent" || quietFlagCluster.MatchString(w) {
			return true
		}
	}
	return false
}

// stripShellComment drops the comment of one physical line, if any.
func stripShellComment(line string) string {
	var single, double bool
	rs := []rune(line)
	for i, c := range rs {
		switch {
		case single:
			single = c != '\''
		case double:
			double = c != '"'
		case c == '\'':
			single = true
		case c == '"':
			double = true
		case c == '#' && (i == 0 || rs[i-1] == ' ' || rs[i-1] == '\t'):
			return string(rs[:i])
		}
	}
	return line
}

// pipeIntoQuietGrep returns the 1-based line (the first physical line of the
// logical one) of every pipeline in src that ends in a quiet grep. A logical
// line continues over a trailing `\` and over a trailing `|`, so `a |`
// followed by `grep -q x` on the next line is one pipeline. allowMarker
// honours the pipeMarker comment on any physical line of the logical one.
func pipeIntoQuietGrep(src string, allowMarker bool) []int {
	var hits []int
	lines := strings.Split(src, "\n")
	for i := 0; i < len(lines); i++ {
		start := i
		var logical strings.Builder
		marked := false
		for ; i < len(lines); i++ {
			if strings.Contains(lines[i], pipeMarker) {
				marked = true
			}
			code := strings.TrimRight(stripShellComment(lines[i]), " \t\r")
			cont := false
			switch {
			case strings.HasSuffix(code, "\\"):
				code, cont = strings.TrimSuffix(code, "\\"), true
			case strings.HasSuffix(code, "|") && !strings.HasSuffix(code, "||"):
				cont = true
			}
			logical.WriteString(code)
			logical.WriteString(" ")
			if !cont {
				break
			}
		}
		if marked && allowMarker {
			continue
		}
		for _, c := range shellCommands(logical.String()) {
			if c.piped && isQuietGrep(c.words) {
				hits = append(hits, start+1)
				break
			}
		}
	}
	return hits
}

// TestPipeIntoQuietGrepShapes pins what the guard sees: every shape of a
// pipeline whose exit status is tested through a quiet grep, and the shapes
// that look alike and are fine.
func TestPipeIntoQuietGrepShapes(t *testing.T) {
	flagged := map[string]string{
		"plain":                   `if echo "$x" | grep -q foo; then :; fi`,
		"flag cluster":            `echo "$x" | grep -Eqi 'a|b'`,
		"flags after the pattern": `echo "$x" | grep -E "a|b" -q`,
		"long flag":               `echo "$x" | grep --quiet foo`,
		"silent":                  `echo "$x" | grep --silent foo`,
		"command prefix":          `echo "$x" | command grep -q foo`,
		"path to grep":            `echo "$x" | /usr/bin/grep -q foo`,
		"egrep":                   `cat f | egrep -q 'a|b'`,
		"second stage":            `a | grep -v '^#' | grep -Eq 'a|b'`,
		"behind and":              `curl -sf "$u" | grep -q '"ready":true' && { ready=1; break; }`,
		"negated":                 `if ! printf '%s' "$h" | grep -q boot; then :; fi`,
		"stderr merged":           `a 2>&1 | grep -q foo`,
		"pipe ends the line":      "a |\n  grep -q foo",
		"pipe and backslash":      "a | \\\n  grep -q foo",
		"backslash before pipe":   "a \\\n  | grep -q foo",
		"in a subshell":           `( a | grep -q foo )`,
		"marker outside selftest": `a | grep -q foo # ` + pipeMarker,
	}
	for name, src := range flagged {
		if got := pipeIntoQuietGrep(src, false); len(got) != 1 || got[0] != 1 {
			t.Errorf("%s: not flagged (hits %v): %q", name, got, src)
		}
	}
	fine := map[string]string{
		"here-string":             `grep -q foo <<<"$x"`,
		"file operand":            `grep -q foo "$f"`,
		"value capture":           `x="$(cmd | head -1)"`,
		"not quiet":               `echo "$x" | grep foo`,
		"count":                   `echo "$x" | grep -c foo`,
		"pattern with a pipe":     `echo "$x" | grep -E 'a|b'`,
		"quiet grep feeds a pipe": `grep -q foo "$f" | wc -l`,
		"or, not a pipe":          `a || grep -q foo "$f"`,
		"and, not a pipe":         `a && grep -q foo "$f"`,
		"after a semicolon":       `a | b; grep -q foo "$f"`,
		"inside a string":         `echo "a | grep -q b"`,
		"inside single quotes":    `echo 'a | grep -q b'`,
		"after double dash":       `echo "$x" | grep -e foo -- -q`,
		"comment line":            `# a | grep -q b`,
		"trailing comment":        `a | b # | grep -q c`,
		"pipe then other":         "a |\n  sort",
		"not a flag":              `echo "$x" | grep -E 'q' -A2`,
	}
	for name, src := range fine {
		if got := pipeIntoQuietGrep(src, false); len(got) != 0 {
			t.Errorf("%s: flagged at %v: %q", name, got, src)
		}
	}
	// The marker is honoured where the caller allows it, and only there.
	marked := `a | grep -q foo # ` + pipeMarker
	if got := pipeIntoQuietGrep(marked, true); len(got) != 0 {
		t.Errorf("marker not honoured where allowed: %v", got)
	}
	if got := pipeIntoQuietGrep("a |\n  grep -q foo # "+pipeMarker, true); len(got) != 0 {
		t.Errorf("marker on the continuation line not honoured where allowed: %v", got)
	}
	if got := pipeIntoQuietGrep("x=1 # "+pipeMarker+"\na | grep -q foo", true); len(got) != 1 || got[0] != 2 {
		t.Errorf("a marker on another logical line must not excuse this one: %v", got)
	}
}

// TestShellScriptsNoPipeIntoGrepQuiet: the e2e scenarios run under
// `set -o pipefail` (run.sh sets it, lib/ and scenarios/ inherit it), and
// `x | grep -q` exits on the first match while x may still be writing, so x
// dies of SIGPIPE and a passing check reads as a miss (and a not-discloses
// check passes on a leak: #1908). The helpers and the scenarios use a
// here-string, `grep -q … <<<"$x"`. test/e2e/lib/selftest.sh pins the helpers
// with a body larger than a pipe; this test keeps the pattern out of every
// script that runs with pipefail: all of test/e2e and test/lib (sourced by
// them), and any other script under test/ that sets it. The POSIX sh suites
// (test/install) have no pipefail and are left alone. The one line that shows
// the trap on purpose, in test/e2e/lib/selftest.sh, carries `e2e-pipe-ok`.
func TestShellScriptsNoPipeIntoGrepQuiet(t *testing.T) {
	root := repoRoot(t)
	scanned, pipefail := 0, 0
	err := filepath.WalkDir(filepath.Join(root, "test"), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".sh") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		sets := pipefailSet.Match(b)
		inherits := strings.HasPrefix(rel, "test/e2e/") || strings.HasPrefix(rel, "test/lib/")
		if !sets && !inherits {
			return nil
		}
		scanned++
		if sets {
			pipefail++
		}
		lines := strings.Split(string(b), "\n")
		for _, n := range pipeIntoQuietGrep(string(b), rel == pipeMarkerFile) {
			t.Errorf("%s:%d: a pipe into a quiet grep fails a passing check under pipefail (SIGPIPE); use a here-string: grep -q … <<<\"$x\"\n  %s", rel, n, strings.TrimSpace(lines[n-1]))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if scanned == 0 || pipefail == 0 {
		t.Fatalf("the guard read %d scripts, %d of them with pipefail: it reads nothing", scanned, pipefail)
	}
}
