package main

// Push access of a KB's remote (#1999). The server commits every write and
// pushes it; a credential that can read the remote but not push it is found,
// without this, only at the first write, as an unpushed commit. `setup` already
// asks the question (gitx.ProbePushAccess: a push --dry-run of a throw-away
// commit, made in a temporary repository, that writes nothing to the remote and
// leaves nothing in the KB's clone). Here the same probe runs where a KB
// arrives (`kb clone`) and where a machine is diagnosed (`doctor`), with the
// environment the SERVER pushes that KB with: the machine's own credentials may
// differ from the ones in the credentials directory. The server start does not
// probe: it has no network to spend there.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/gitx"
)

// pushProbeTimeout bounds one probe: an unreachable remote costs this much, and
// doctor probes the KBs side by side so it costs it once.
const pushProbeTimeout = 20 * time.Second

// probePushAccessFn is indirected so a test can play a remote without a forge.
var probePushAccessFn = gitx.ProbePushAccess

type pushVerdict int

const (
	// pushWritable: the remote accepted the dry run.
	pushWritable pushVerdict = iota
	// pushNoRemote: the clone has no origin, so there is nothing to push to.
	pushNoRemote
	// pushRefused: the remote answered and the credential cannot push (or is
	// not accepted at all); Err says why, with the remedy.
	pushRefused
	// pushOffline: the remote did not answer (gitx.IsRemoteUnreachable): no
	// verdict on the credential.
	pushOffline
)

type pushResult struct {
	Verdict pushVerdict
	Err     error
}

// kbPushEnv is the git environment the server pushes the KB name with: the KB's
// own token and key (gitEnvForKB), and default.ssh through GIT_SSH_COMMAND
// unless the process environment or the KB already sets one (setupGitSSH).
func kbPushEnv(name, credDir string) []string {
	env := gitEnvForKB(name, credDir)
	for _, e := range env {
		if strings.HasPrefix(e, "GIT_SSH_COMMAND=") {
			return env
		}
	}
	if _, set := os.LookupEnv("GIT_SSH_COMMAND"); set {
		return env
	}
	if key, ok := credentialFile(credDir, "", config.CredentialSSH); ok {
		env = append(env, "GIT_SSH_COMMAND="+sshCommandWithHosts(credDir, key))
	}
	return env
}

// serverCredentialsDir is the credentials directory of the server whose data
// directory is dataDir: credentials_dir of the server file at configPath, else
// <dataDir>/credentials. configPath may be "" or unreadable.
func serverCredentialsDir(configPath, dataDir string) string {
	c := &config.Config{Data: dataDir}
	if configPath != "" {
		if cfg, err := config.Load(configPath); err == nil {
			c.CredentialsDir = cfg.CredentialsDir
		}
	}
	return c.CredentialsPath()
}

// probeKBPush asks the origin of the KB clone at path whether the server's
// credentials for the KB name may push to it.
func probeKBPush(ctx context.Context, name, path, credDir string) pushResult {
	remote, err := gitx.RemoteURL(path, "origin")
	if err != nil || remote == "" {
		return pushResult{Verdict: pushNoRemote}
	}
	err = probePushAccessFn(ctx, remote, kbPushEnv(name, credDir)...)
	switch {
	case err == nil:
		return pushResult{Verdict: pushWritable}
	case gitx.IsRemoteUnreachable(err):
		return pushResult{Verdict: pushOffline, Err: err}
	}
	return pushResult{Verdict: pushRefused, Err: err}
}

// firstLine is the headline of a multi-line git error, safe to print.
func firstLine(err error) string {
	line, _, _ := strings.Cut(err.Error(), "\n")
	return escapeControl(strings.TrimSpace(line))
}

// warnIfCannotPush is the probe `kb clone` runs on the clone it just made. It
// only says what it found: a reader may clone on purpose, so the KB stays
// mounted and the exit code stays 0.
func warnIfCannotPush(name, path, configPath, dataDir string) {
	ctx, cancel := context.WithTimeout(context.Background(), pushProbeTimeout)
	defer cancel()
	// git runs in its own process group: Ctrl-C reaches it only through ctx.
	stop := onInterrupt(cancel)
	defer stop()
	res := probeKBPush(ctx, name, path, serverCredentialsDir(configPath, dataDir))
	switch res.Verdict {
	case pushRefused:
		fmt.Fprintf(os.Stderr, "Warning: the credentials this server pushes %q with can read its remote but cannot push to it: %s\n", name, firstLine(res.Err))
		fmt.Fprintln(os.Stderr, "  Writes will be committed in the clone and never reach the remote. To write, give the KB a credential with write access (<kb>.token or <kb>.ssh in the server's credentials directory); to only read, ignore this. `cartographer doctor` checks it again.")
	case pushOffline:
		fmt.Fprintf(os.Stderr, "Note: push access of %q was not checked, the remote did not answer (%s); `cartographer doctor` checks it later\n", name, firstLine(res.Err))
	}
}

// doctorServerDirsFn is the data and credentials directories of the local
// server; a variable so a doctor test does not read this machine's.
var doctorServerDirsFn = func() (dataDir, credDir string) {
	dataDir = resolveServerDataDir("")
	configPath, _ := serverConfigFile("")
	return dataDir, serverCredentialsDir(configPath, dataDir)
}

// checkPushAccess: a KB whose remote refuses the push of the credentials the
// server uses fills the clone with commits that never leave it, and nothing
// says so until the first write (#1999). It probes each KB of the LOCAL server
// that has an origin, side by side; a remote that does not answer is an info
// finding, not a verdict. It stays silent for a server on another machine (its
// clones are not here to read), and when the server did not answer. It writes
// nothing: the probe works in a temporary repository.
func checkPushAccess(cfg *clientconfig.Config, facts serverFacts, healthErr error) []doctorFinding {
	if healthErr != nil || !isLoopbackURL(cfg.ServerURL) || len(facts.Names) == 0 {
		return nil
	}
	dataDir, credDir := doctorServerDirsFn()
	results := make([]pushResult, len(facts.Names))
	var wg sync.WaitGroup
	for i, name := range facts.Names {
		if validateKBName(name) != nil {
			results[i].Verdict = pushNoRemote
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), pushProbeTimeout)
			defer cancel()
			results[i] = probeKBPush(ctx, name, filepath.Join(dataDir, name), credDir)
		}()
	}
	wg.Wait()
	var out []doctorFinding
	for i, name := range facts.Names {
		path := filepath.Join(dataDir, name)
		switch r := results[i]; r.Verdict {
		case pushRefused:
			out = append(out, doctorFinding{
				Check: "push-access", Severity: doctorWarning, Path: path,
				Message: fmt.Sprintf("KB %q: the credentials the server pushes it with cannot push to its remote: %s", escapeControl(name), firstLine(r.Err)),
				Fix:     "give the KB a credential with write access (<kb>.token or <kb>.ssh in the credentials directory, then restart the service); ignore this if the KB is for reading",
			})
		case pushOffline:
			out = append(out, doctorFinding{
				Check: "push-access", Severity: doctorInfo, Path: path,
				Message: fmt.Sprintf("KB %q: push access not checked, the remote did not answer (%s)", escapeControl(name), firstLine(r.Err)),
				Fix:     "run `cartographer doctor` again when the remote is reachable",
			})
		}
	}
	return out
}
