package service

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A run whose HOME is not the account's home never reaches the per-user
// scheduler (#683): the e2e suite's sandboxed `connect` replaced a user's real
// sync timer this way.
func TestSchedulerRefusedUnderSandboxedHome(t *testing.T) {
	defer func(a, e func() (string, error)) { accountHome, envHome = a, e }(accountHome, envHome)
	accountHome = func() (string, error) { return "/home/user", nil }

	envHome = func() (string, error) { return "/tmp/sandbox", nil }
	for _, cmd := range []string{"launchctl", "systemctl", "powershell.exe", "wsl.exe", "/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe"} {
		if _, err := execRun(cmd, "print"); !errors.Is(err, errSandboxHome) {
			t.Errorf("%s under a sandboxed HOME: err = %v, want errSandboxHome", cmd, err)
		}
	}
	// The text says what was refused, not what the caller was doing: a read
	// through interop (`doctor` reading .wslconfig) has no unit files.
	err := guardScheduler("/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe")
	if err == nil {
		t.Fatal("powershell.exe under a sandboxed HOME was not refused")
	}
	for _, want := range []string{"HOME=/tmp/sandbox", "account home /home/user", "powershell.exe was not run, so nothing outside this HOME was changed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	// Nor does it claim more than every scheduler command can promise: loginctl
	// changes the login manager and wsl.exe/powershell.exe may only read.
	for _, bad := range []string{"unit files", "written", "registered", "per-user scheduler"} {
		if strings.Contains(err.Error(), bad) {
			t.Errorf("error %q is install-specific (%q)", err, bad)
		}
	}
	if err := guardScheduler("git"); err != nil {
		t.Errorf("a non-scheduler command is not guarded: %v", err)
	}

	envHome = func() (string, error) { return "/home/user/", nil }
	if err := guardScheduler("launchctl"); err != nil {
		t.Errorf("HOME is the account home: err = %v, want nil", err)
	}
}

// stubSandboxedExecutable points osExecutable at a real file, so an install
// gets as far as the scheduler.
func stubSandboxedExecutable(t *testing.T) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "cartographer")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := osExecutable
	osExecutable = func() (string, error) { return bin, nil }
	t.Cleanup(func() { osExecutable = orig })
}

// The guard's own text is neutral (it also refuses reads), so the install is
// the one to say that it wrote the unit files and registered nothing -- and the
// wrap keeps errors.Is(err, ErrSandboxHome), which connect relies on.
func TestInstallUnderSandboxedHomeSaysTheFilesAreWritten(t *testing.T) {
	for _, platform := range []string{"darwin", "linux"} {
		t.Run(platform+"/service", func(t *testing.T) {
			home := withTestHome(t, platform)
			stubSandboxedExecutable(t)
			_, err := NewManager().Install(InstallOptions{DataDir: filepath.Join(home, "data"), HTTPAddr: "127.0.0.1:39273"})
			if !errors.Is(err, ErrSandboxHome) {
				t.Fatalf("Install under a sandboxed HOME: err = %v, want ErrSandboxHome", err)
			}
			if !strings.Contains(err.Error(), unitsWrittenNote) {
				t.Errorf("error %q does not say the unit files are written", err)
			}
			if def, derr := DefinitionPath(); derr != nil || !fileExists(def) {
				t.Errorf("the unit file the error talks about is missing (%q, %v)", def, derr)
			}
		})
		t.Run(platform+"/sync-timer", func(t *testing.T) {
			withTestHome(t, platform)
			stubSandboxedExecutable(t)
			err := NewManager().InstallSyncTimer(0)
			if !errors.Is(err, ErrSandboxHome) {
				t.Fatalf("InstallSyncTimer under a sandboxed HOME: err = %v, want ErrSandboxHome", err)
			}
			if !strings.Contains(err.Error(), unitsWrittenNote) {
				t.Errorf("error %q does not say the unit files are written", err)
			}
			path, perr := SyncLaunchdPlistPath()
			if platform == "linux" {
				path, perr = SyncSystemdTimerPath()
			}
			if perr != nil || !fileExists(path) {
				t.Errorf("the sync unit the error talks about is missing (%q, %v)", path, perr)
			}
		})
	}
}

// A refusal that is not an install's -- a probe, a read through interop -- says
// nothing about unit files, and sandboxRefusal leaves other errors alone.
func TestSandboxRefusalOnlyAddsToTheGuardsError(t *testing.T) {
	withTestHome(t, "linux")
	_, err := execRun("powershell.exe", "-NoProfile")
	if !errors.Is(err, errSandboxHome) || strings.Contains(err.Error(), "unit files") {
		t.Errorf("a bare refusal = %v, want errSandboxHome without a word on unit files", err)
	}
	other := errors.New("boom")
	if got := sandboxRefusal(other, unitsWrittenNote); got != other {
		t.Errorf("a non-guard error was rewrapped: %v", got)
	}
	if sandboxRefusal(nil, unitsWrittenNote) != nil {
		t.Error("nil became an error")
	}
}
