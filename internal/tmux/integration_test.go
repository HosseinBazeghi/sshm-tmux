package tmux

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// installFakeSSH installs a fake ssh that logs its arguments and emits
// canned output, so tests need no network.
func installFakeSSH(t *testing.T, output string, exitCode int) string {
	t.Helper()
	dir := t.TempDir()
	logFile := filepath.Join(dir, "args.log")
	script := filepath.Join(dir, "ssh")

	scriptContent := "#!/bin/sh\necho \"$@\" >> " + logFile + "\n" +
		"printf '%b' '" + output + "' >&2\n" +
		"exit " + strconv.Itoa(exitCode) + "\n"
	if err := os.WriteFile(script, []byte(scriptContent), 0o755); err != nil {
		t.Fatalf("could not write fake ssh: %v", err)
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logFile
}

// installShellSSH installs a fake ssh that runs the remote command through a
// real shell (sh -c), plus fake tmux and infocmp binaries, to catch
// remote-shell quoting bugs.
func installShellSSH(t *testing.T) (sshLog, tmuxLog string) {
	t.Helper()
	dir := t.TempDir()
	sshLog = filepath.Join(dir, "ssh_args.log")
	tmuxLog = filepath.Join(dir, "tmux_args.log")

	fakeSSH := `#!/usr/bin/env python3
import subprocess, sys

args = sys.argv[1:]
with open("` + sshLog + `", "a") as f:
    f.write(" ".join(args) + "\n")

# skip ssh options and their values, then split host / remote command
i = 0
while i < len(args) and args[i].startswith("-"):
    if args[i] in ("-F", "-o"):
        i += 2
    else:
        i += 1
cmd = " ".join(args[i + 1:])
r = subprocess.run(["sh", "-c", cmd], capture_output=True, text=True)
sys.stdout.write(r.stdout)
sys.stderr.write(r.stderr)
sys.exit(r.returncode)
`
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(fakeSSH), 0o755); err != nil {
		t.Fatalf("could not write fake ssh: %v", err)
	}

	fakeTmux := "#!/bin/sh\necho \"$@\" >> " + tmuxLog + "\n" +
		"# real tmux rejects 'list-sessions -F' without its argument\n" +
		"if [ \"$1\" = \"list-sessions\" ] && [ \"$2\" = \"-F\" ] && [ \"$#\" -eq 2 ]; then\n" +
		"  echo \"command list-sessions: -F expects an argument\" >&2\n" +
		"  exit 1\n" +
		"fi\n" +
		"echo dev\n" +
		"echo editor\n"
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(fakeTmux), 0o755); err != nil {
		t.Fatalf("could not write fake tmux: %v", err)
	}

	fakeInfocmp := "#!/bin/sh\n[ \"$1\" = \"good-term\" ]\n"
	if err := os.WriteFile(filepath.Join(dir, "infocmp"), []byte(fakeInfocmp), 0o755); err != nil {
		t.Fatalf("could not write fake infocmp: %v", err)
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return sshLog, tmuxLog
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("could not read log %s: %v", path, err)
	}
	return strings.TrimSpace(string(data))
}

func TestProbeHostParsesSessionsAndTerm(t *testing.T) {
	logFile := installFakeSSH(t,
		"dev\neditor\nsshm.list:0\nsshm.term:1\n", 0)

	probe := ProbeHost("myhost", "", "", "xterm-ghostty")

	if len(probe.Sessions) != 2 || probe.Sessions[0] != "dev" || probe.Sessions[1] != "editor" {
		t.Errorf("Expected [dev editor], got %v", probe.Sessions)
	}
	if probe.ListError != "" || probe.ActionError != "" {
		t.Errorf("Unexpected errors: %+v", probe)
	}
	if !probe.TermChecked || probe.TermSupported {
		t.Errorf("Expected checked but unsupported terminal, got %+v", probe)
	}

	// The whole probe must be a single ssh invocation
	want := "-o BatchMode=yes -o ConnectTimeout=5 myhost " +
		"tmux list-sessions -F '#{session_name}' 2>&1; echo 'sshm.list:'$?; " +
		"infocmp 'xterm-ghostty' >/dev/null 2>&1; echo 'sshm.term:'$?"
	if got := readLog(t, logFile); got != want {
		t.Errorf("ssh args = %q, want %q", got, want)
	}
}

func TestProbeHostNoServerRunning(t *testing.T) {
	installFakeSSH(t, "no server running on /tmp/tmux-1000/default\nsshm.list:1\n", 0)

	probe := ProbeHost("myhost", "", "", "")
	if probe.ListError != "" {
		t.Fatalf("no tmux server must not be an error, got: %q", probe.ListError)
	}
	if len(probe.Sessions) != 0 {
		t.Errorf("Expected no sessions, got %v", probe.Sessions)
	}
	if probe.TermChecked {
		t.Error("Terminal must not be checked when TERM is empty")
	}
}

func TestProbeHostListFailureIsReported(t *testing.T) {
	installFakeSSH(t, "tmux: command not found\nsshm.list:127\n", 0)

	probe := ProbeHost("myhost", "", "", "")
	if probe.ListError == "" {
		t.Error("Expected a list error when tmux is unavailable on the host")
	}
	if probe.Sessions != nil {
		t.Errorf("Expected no sessions, got %v", probe.Sessions)
	}
}

func TestProbeHostTransportFailureIsReported(t *testing.T) {
	installFakeSSH(t, "ssh: connect to host myhost port 22: Connection refused", 255)

	probe := ProbeHost("myhost", "", "", "")
	if probe.ListError == "" {
		t.Error("Expected a list error when the host is unreachable")
	}
	if !strings.Contains(probe.ListError, "Connection refused") {
		t.Errorf("Expected the ssh error to be surfaced, got %q", probe.ListError)
	}
}

func TestProbeHostReportsActionError(t *testing.T) {
	installFakeSSH(t,
		"duplicate session: dev\nsshm.action:1\nwork\nsshm.list:0\n", 0)

	action, err := NewSessionCommand("dev")
	if err != nil {
		t.Fatalf("NewSessionCommand unexpected error: %v", err)
	}
	probe := ProbeHost("myhost", "", action, "")

	if probe.ActionError != "duplicate session: dev" {
		t.Errorf("Expected the action error, got %q", probe.ActionError)
	}
	if len(probe.Sessions) != 1 || probe.Sessions[0] != "work" {
		t.Errorf("List must still be parsed, got %v", probe.Sessions)
	}
}

// TestProbeHostThroughRemoteShell is a regression test: ssh hands the command
// to the remote shell, where an unquoted #{session_name} is stripped as a
// comment and tmux fails with "-F expects an argument".
func TestProbeHostThroughRemoteShell(t *testing.T) {
	_, tmuxLog := installShellSSH(t)

	probe := ProbeHost("myhost", "", "", "xterm-ghostty")

	if len(probe.Sessions) != 2 || probe.Sessions[0] != "dev" || probe.Sessions[1] != "editor" {
		t.Fatalf("Expected [dev editor], got %v (err: %q)", probe.Sessions, probe.ListError)
	}
	if !probe.TermChecked || probe.TermSupported {
		t.Errorf("Expected checked but unsupported terminal, got %+v", probe)
	}

	// The format string must survive the remote shell and reach tmux
	if got := readLog(t, tmuxLog); got != "list-sessions -F #{session_name}" {
		t.Errorf("tmux args = %q, want %q (format string was mangled by the shell)",
			got, "list-sessions -F #{session_name}")
	}
}

func TestSessionActionsThroughRemoteShell(t *testing.T) {
	sshLog, tmuxLog := installShellSSH(t)

	action, err := NewSessionCommand("dev")
	if err != nil {
		t.Fatalf("NewSessionCommand unexpected error: %v", err)
	}
	if probe := ProbeHost("myhost", "", action, ""); probe.ActionError != "" {
		t.Fatalf("NewSession action failed: %q", probe.ActionError)
	}
	if probe := ProbeHost("myhost", "", KillSessionCommand("my-session"), ""); probe.ActionError != "" {
		t.Fatalf("KillSession action failed: %q", probe.ActionError)
	}

	tmuxCalls := strings.Split(readLog(t, tmuxLog), "\n")
	if len(tmuxCalls) != 4 {
		t.Fatalf("Expected 4 tmux invocations, got %d: %v", len(tmuxCalls), tmuxCalls)
	}
	wantCalls := []string{
		"new-session -d -s dev",
		"list-sessions -F #{session_name}",
		"kill-session -t my-session",
		"list-sessions -F #{session_name}",
	}
	for i, want := range wantCalls {
		if tmuxCalls[i] != want {
			t.Errorf("tmux call %d = %q, want %q", i, tmuxCalls[i], want)
		}
	}

	// Each mutation and its list reload share a single ssh round-trip
	sshCalls := strings.Split(readLog(t, sshLog), "\n")
	if len(sshCalls) != 2 {
		t.Fatalf("Expected 2 ssh invocations (one per probe), got %d: %v", len(sshCalls), sshCalls)
	}
	if !strings.Contains(sshCalls[0], "new-session -d -s 'dev'") ||
		!strings.Contains(sshCalls[0], "list-sessions") {
		t.Errorf("Action and list must share one round-trip: %q", sshCalls[0])
	}
}
