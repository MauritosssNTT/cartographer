package repodocs

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// A red gate must say what failed (#1952, #1870): the e2e runner names each failed scenario
// with its first failing assertion, the signoff prints the output of every package a red
// Windows leg failed (not the last 40 lines of the leg), and the lander names the scenarios and
// the panicking packages in the issue it files. These tests pin the three ends of that chain.

// failingOf runs scripts/land.sh's failing() over a gate log.
func failingOf(t *testing.T, body string) string {
	t.Helper()
	needBash(t)
	script := readFile(t, repoRoot(t), "scripts/land.sh")
	start := strings.Index(script, "\nfailing() {")
	end := strings.Index(script[start+1:], "\n}\n")
	if start < 0 || end < 0 {
		t.Fatal("failing() not found in scripts/land.sh")
	}
	fn := script[start : start+1+end+3]
	logFile := filepath.Join(t.TempDir(), "gate.log")
	if err := os.WriteFile(logFile, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("bash", "-c", fn+"\nfailing \"$1\"", "bash", logFile).CombinedOutput()
	if err != nil {
		t.Fatalf("failing: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// The e2e runner's report lists every failed scenario by name and first failing line, in the
// shape the lander reads, and shows the failing scenario's [FAIL] lines after the report.
func TestE2ERunnerNamesEachFailedScenario(t *testing.T) {
	needBash(t)
	if runtime.GOOS == "windows" {
		t.Skip("bash script")
	}
	root := repoRoot(t)
	tmp := t.TempDir()
	e2e := filepath.Join(tmp, "test", "e2e")
	write := func(path, body string, mode os.FileMode) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(e2e, "run.sh"), readFile(t, root, "test/e2e/run.sh"), 0o755)
	write(filepath.Join(tmp, "bin", "make"), "#!/bin/sh\nexit 0\n", 0o755) // run.sh builds first
	sc := filepath.Join(e2e, "scenarios")
	write(filepath.Join(sc, "01_green.sh"), "echo '[PASS] fine'\nexit 0\n", 0o755)
	write(filepath.Join(sc, "02_two_asserts.sh"), "echo '[PASS] setup'\necho '[FAIL] first thing broke'\necho '[FAIL] second thing broke'\nexit 1\n", 0o755)
	write(filepath.Join(sc, "03_crash.sh"), "echo 'starting'\necho 'server did not start'\nexit 3\n", 0o755)
	write(filepath.Join(sc, "04_other.sh"), "echo '[FAIL] another one' >&2\nexit 1\n", 0o755)

	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") && !strings.HasPrefix(kv, "E2E_") && !strings.HasPrefix(kv, "TMPDIR=") {
			env = append(env, kv)
		}
	}
	// Run outside any repository: run.sh checks that git has no identity, and a clone may set one.
	run := func() ([]byte, error) {
		cmd := exec.Command("bash", filepath.Join(e2e, "run.sh"))
		cmd.Dir = tmp
		cmd.Env = append(env, "TMPDIR="+tmp, "PATH="+filepath.Join(tmp, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
		return cmd.CombinedOutput()
	}
	b, err := run()
	out := string(b)
	if err == nil {
		t.Fatalf("failed scenarios must fail the runner\n%s", out)
	}
	for _, want := range []string{
		"E2E FAILED: 02_two_asserts: [FAIL] first thing broke\n",
		"E2E FAILED: 03_crash: no [FAIL] line, exit 3: server did not start\n",
		"E2E FAILED: 04_other: [FAIL] another one\n",
		" Failed  : 3\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report lacks %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "E2E FAILED: 01_green") {
		t.Errorf("a green scenario is not named\n%s", out)
	}
	// After the report: the [FAIL] lines of the failed scenario again, so a cut of the middle
	// of the log keeps them.
	report := strings.Index(out, " E2E Report")
	if report < 0 || !strings.Contains(out[report:], "[FAIL] second thing broke") {
		t.Errorf("the failed scenario's [FAIL] lines must follow the report\n%s", out)
	}

	// All green: nothing is named.
	for _, f := range []string{"02_two_asserts", "03_crash", "04_other"} {
		if err := os.Remove(filepath.Join(sc, f+".sh")); err != nil {
			t.Fatal(err)
		}
	}
	b, err = run()
	if err != nil || strings.Contains(string(b), "E2E FAILED") {
		t.Errorf("a green run names nothing and passes: %v\n%s", err, b)
	}
}

// failing() names the e2e scenarios, the panicking packages and the tests of a red log, at
// most 10, each test once, in the charset an issue title is built from.
func TestLanderFailingNamesScenariosAndPanics(t *testing.T) {
	const (
		e2e = " E2E Report\n Failed  : 2\n" +
			"E2E FAILED: 03_config_opencode: [FAIL] file not found: x\n" +
			"E2E FAILED: 15_operational_audit: [FAIL] no key\n"
		testPanic = "--- FAIL: TestBridge (0.00s)\npanic: boom [recovered]\n\tpanic: boom\n\ngoroutine 7 [running]:\nFAIL\tgithub.com/x/p/internal/mcpbridge\t0.5s\n"
	)
	for _, c := range []struct{ name, log, want string }{
		{"two scenarios", e2e + "make: *** [Makefile:188: e2e] Error 1\n",
			"e2e.03_config_opencode e2e.15_operational_audit"},
		{"a panic in a test names the package and the test, once", "ok  \tgithub.com/x/p/internal/a\t0.1s\n" + testPanic,
			"mcpbridge.TestBridge (panic)"},
		{"a panic outside a test names the package and the message",
			"panic: runtime error: invalid memory address [recovered]\ngoroutine 1 [running]:\nFAIL\tgithub.com/x/p/internal/kb\t0.5s\n",
			"kb (panic: runtime error: invalid memory address)"},
		{"a timeout names the running test", "panic: test timed out after 20m0s\n\trunning tests:\n\t\tTestSlow (20m0s)\nFAIL\tgithub.com/x/p/internal/slow\t1200s\n",
			"slow.TestSlow (panic)"},
		{"a frame of the trace names the test", "panic: oops\ngoroutine 5 [running]:\ngithub.com/x/p/internal/frame.TestFr(0xc000)\nFAIL\tgithub.com/x/p/internal/frame\t0.1s\n",
			"frame.TestFr (panic)"},
		{"a panic of a package that passes is not one", "panic: expected and harmless\nok  \tgithub.com/x/p/internal/fine\t0.1s\n", ""},
		{"a message is cut to the title charset", "panic: bad `$(id)` ; rm\nFAIL\tgithub.com/x/p/internal/kb\t0.5s\n", "kb (panic: bad id  rm)"},
		{"the same log, the colours and CRLF of a Windows leg", "\x1b[31m" + strings.ReplaceAll(testPanic, "\n", "\r\n") + "\x1b[0m" + e2e,
			"mcpbridge.TestBridge (panic) e2e.03_config_opencode e2e.15_operational_audit"},
		{"a plain test still named", "--- FAIL: TestPlain (0.01s)\nFAIL\tgithub.com/x/p/internal/kb\t0.1s\n" + e2e,
			"e2e.03_config_opencode e2e.15_operational_audit TestPlain"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := failingOf(t, c.log); got != c.want {
				t.Errorf("failing() = %q, want %q", got, c.want)
			}
		})
	}
	t.Run("at most ten", func(t *testing.T) {
		var b strings.Builder
		for i := 0; i < 13; i++ {
			b.WriteString("E2E FAILED: s" + strconv.Itoa(i) + ": [FAIL] x\n")
		}
		if n := len(strings.Fields(failingOf(t, b.String()))); n != 10 {
			t.Errorf("%d names, want 10", n)
		}
	})
}

// The planner of the signoff's targeted retry never plans a panic or an e2e failure as a Go
// test to run again (its header says so): the new lines the log holds do not widen it.
func TestTestFailuresDoesNotPlanPanicsOrE2EScenarios(t *testing.T) {
	needBash(t)
	for _, c := range []struct{ name, log string }{
		{"e2e report", "E2E FAILED: 03_config_opencode: [FAIL] x\nmake: *** [Makefile:188: e2e] Error 1\n"},
		{"e2e report beside a named test", "--- FAIL: TestA (0.01s)\nFAIL\tgithub.com/x/p/internal/kb\t0.2s\n" +
			"E2E FAILED: 03_config_opencode: [FAIL] x\nmake: *** [Makefile:188: e2e] Error 1\nmake: *** [Makefile:23: test] Error 1\n"},
		{"a Windows panic among passing packages", "ok  \tgithub.com/x/p/internal/a\t0.1s\n--- FAIL: TestBridge (0.00s)\n" +
			"panic: boom [recovered]\n\tpanic: boom\n\ngoroutine 7 [running]:\nFAIL\tgithub.com/x/p/internal/mcpbridge\t0.5s\nok  \tgithub.com/x/p/internal/z\t0.1s\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			log := filepath.Join(t.TempDir(), "gate.log")
			if err := os.WriteFile(log, []byte(c.log), 0o644); err != nil {
				t.Fatal(err)
			}
			err := exec.Command("bash", filepath.Join(repoRoot(t), "scripts", "test-failures.sh"), log).Run()
			ee, ok := err.(*exec.ExitError)
			if !ok || ee.ExitCode() != 3 {
				t.Errorf("want exit 3 (nothing a retry covers), got %v", err)
			}
		})
	}
}

// A red Windows leg prints the output of the packages that failed, from their first failing
// line: the `panic:` header and an earlier package are not lost to a tail of the whole log, and
// the passing packages' output stays out of it. A panic is not retried.
func TestSignoffPrintsTheFailingPackagesOfTheWindowsLeg(t *testing.T) {
	e := newSignoffEnv(t)
	e.goModule()
	e.branch(e.work, "feat/x", "internal/a/a2.go")
	e.write(filepath.Join(e.work, "internal", "a", "a2.go"), "package a\n", 0o644)
	e.run(e.work, "git", "commit", "-q", "-am", "a2")
	fakewin := filepath.Join(filepath.Dir(e.winLog), "bin", "fakewin")

	var b strings.Builder
	b.WriteString("ok  \\tgithub.com/x/p/internal/a\\t0.1s\\r\\n")
	// A package that failed before the panicking one, with the noise a test prints before its verdict.
	b.WriteString("starting a server\\r\\n--- FAIL: TestFirst (0.10s)\\r\\n    first_test.go:9: got 1, want 2\\r\\nFAIL\\r\\nFAIL\\tgithub.com/x/p/internal/first\\t0.2s\\r\\n")
	b.WriteString("noise of the panicking package, before its failure\\r\\n--- FAIL: TestBridge (0.00s)\\r\\npanic: boom [recovered]\\r\\n\\tpanic: boom\\r\\n\\r\\ngoroutine 7 [running]:\\r\\n")
	for i := 0; i < 80; i++ {
		b.WriteString("frame-" + strconv.Itoa(i) + "\\r\\n")
	}
	b.WriteString("exit status 2\\r\\nFAIL\\tgithub.com/x/p/internal/mcpbridge\\t0.5s\\r\\n")
	// What the old `tail -n 40` showed instead: the output of a passing package that ended the log.
	for i := 0; i < 60; i++ {
		b.WriteString("passing-noise-" + strconv.Itoa(i) + "\\r\\n")
	}
	b.WriteString("ok  \\tgithub.com/x/p/internal/z\\t0.1s\\r\\n")

	out, ok := e.signoff(e.work, "SIGNOFF_WINDOWS="+fakewin, "FAKE_WIN_RC=1", "FAKE_WIN_OUT="+b.String())
	if ok || !strings.Contains(out, "the Windows leg failed") {
		t.Fatalf("a red Windows leg must fail the signoff: ok=%v\n%s", ok, out)
	}
	for _, want := range []string{
		"--- FAIL: TestFirst (0.10s)", "first_test.go:9: got 1, want 2", "FAIL\tgithub.com/x/p/internal/first",
		"--- FAIL: TestBridge (0.00s)", "panic: boom [recovered]", "goroutine 7 [running]:", "frame-0\n",
		"lines of the output of github.com/x/p/internal/mcpbridge cut)", "FAIL\tgithub.com/x/p/internal/mcpbridge\t0.5s",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the output lacks %q\n%s", want, out)
		}
	}
	for _, not := range []string{"passing-noise", "noise of the panicking package", "starting a server", "frame-40\n"} {
		if strings.Contains(out, not) {
			t.Errorf("the output holds %q, which is not a failing package's failure (or was cut)\n%s", not, out)
		}
	}
	if got := e.log(e.winLog); strings.Contains(got, "go test -count=1") {
		t.Errorf("a panic is not retried, got %q", got)
	}
	if e.remoteTip("origin", "feat/x") != "" || len(e.posts()) != 0 {
		t.Errorf("nothing may be pushed or posted\n%s", out)
	}
}

// A queued train whose gate failed on two e2e scenarios and passed on the re-run lands, and
// the flaky issue names both scenarios and keeps the first failing line of each (#1952).
func TestLanderNamesTheE2EScenariosOfAFlakyGate(t *testing.T) {
	e := newLandEnv(t)
	e.queue("f", "fix(kb): f\n", "2026-01-01T10:00:00Z", map[string]string{"E2EFLAKY": "x\n"})
	out, ok := e.land()
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "fix(kb): f" {
		t.Fatalf("main holds %q\n%s", got, out)
	}
	gh := e.log()
	if !strings.Contains(gh, "--title flaky: e2e.03_config_opencode e2e.15_operational_audit failed once and passed on a re-run") {
		t.Errorf("the issue title must name both scenarios:\n%s", gh)
	}
	for _, want := range []string{"Failing tests: e2e.03_config_opencode e2e.15_operational_audit.", "    E2E FAILED: 03_config_opencode: [FAIL] file not found: x", "    E2E FAILED: 15_operational_audit: [FAIL] no key"} {
		if !strings.Contains(gh, want) {
			t.Errorf("the issue body lacks %q:\n%s", want, gh)
		}
	}
	if strings.Contains(gh, "none named") {
		t.Errorf("the failing scenarios are named:\n%s", gh)
	}
}

// A Windows leg that panics among passing packages is named by package (and test) in the
// flaky issue, and the signoff's output the lander keeps holds the `panic:` header.
func TestLanderNamesAWindowsPanic(t *testing.T) {
	e := newLandEnv(t)
	e.write(filepath.Join(e.bin, "fakewin"), `#!/bin/sh
echo "$*" >> "$FAKE_STATE.win"
if [ ! -e "$FAKE_STATE.winpanic" ]; then
  touch "$FAKE_STATE.winpanic"
  printf 'ok  \tgithub.com/x/p/internal/a\t0.1s\n--- FAIL: TestBridge (0.00s)\npanic: boom [recovered]\n\tpanic: boom\n\ngoroutine 7 [running]:\nexit status 2\nFAIL\tgithub.com/x/p/internal/mcpbridge\t0.5s\npanic: oops\n\ngoroutine 9 [running]:\nFAIL\tgithub.com/x/p/internal/other\t0.1s\nok  \tgithub.com/x/p/internal/z\t0.1s\n'
  exit 1
fi
exit 0
`)
	e.queue("w", "fix(changelog): w\n", "2026-01-01T10:00:00Z", map[string]string{"internal/changelog/zz.go": "package changelog\n"})
	out, ok := e.land("SIGNOFF_WINDOWS=" + filepath.Join(e.bin, "fakewin"))
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "fix(changelog): w" {
		t.Fatalf("main holds %q\n%s", got, out)
	}
	if !strings.Contains(out, "panic: boom [recovered]") {
		t.Errorf("the gate log must keep the panic header of the Windows leg\n%s", out)
	}
	if gh := e.log(); !strings.Contains(gh, "--title flaky: mcpbridge.TestBridge (panic) other (panic: oops) failed once and passed on a re-run") {
		t.Errorf("the issue title must name the panicking packages:\n%s", gh)
	}
}
