// Package tmux manages tmux sessions on remote hosts over SSH.
package tmux

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// commandTimeout bounds each SSH round-trip to a remote host.
const commandTimeout = 10 * time.Second

// Markers delimit the sections of the combined probe output.
const (
	actionMarker = "sshm.action:"
	listMarker   = "sshm.list:"
	termMarker   = "sshm.term:"
)

var (
	// validSessionNamePattern restricts session names to shell-safe characters.
	validSessionNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

	// validTermPattern restricts TERM values probed on a remote host.
	validTermPattern = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)
)

// ValidateSessionName reports whether name is a usable tmux session name.
func ValidateSessionName(name string) error {
	if name == "" {
		return fmt.Errorf("session name cannot be empty")
	}
	if !validSessionNamePattern.MatchString(name) {
		return fmt.Errorf("session name may only contain letters, digits, '-' and '_'")
	}
	return nil
}

// shellQuote single-quotes s so it survives the remote shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// NewSessionCommand returns the command that creates a detached session.
func NewSessionCommand(sessionName string) (string, error) {
	if err := ValidateSessionName(sessionName); err != nil {
		return "", err
	}
	return "tmux new-session -d -s " + shellQuote(sessionName), nil
}

// KillSessionCommand returns the command that terminates a session.
func KillSessionCommand(sessionName string) string {
	return "tmux kill-session -t " + shellQuote(sessionName)
}

// AttachCommandArgs returns ssh args that attach to session on host,
// creating it when missing (tmux new-session -A).
func AttachCommandArgs(configFile, hostName, sessionName string) []string {
	args := baseArgs(configFile)
	return append(args, "-t", hostName, "tmux", "new-session", "-A", "-s", sessionName)
}

// HostProbe is the outcome of one SSH round-trip to a host.
type HostProbe struct {
	Sessions      []string // tmux session names, nil when there are none
	ListError     string   // non-empty when the session list could not be read
	ActionError   string   // non-empty when a session mutation failed
	TermChecked   bool     // true when the terminfo entry was looked up
	TermSupported bool     // true when the host knows the local terminal
}

// ProbeHost runs one SSH round-trip: an optional tmux action, the session
// list, and a terminfo check for term.
func ProbeHost(hostName, configFile, action, term string) *HostProbe {
	var cmd strings.Builder
	if action != "" {
		cmd.WriteString(action)
		cmd.WriteString(" 2>&1; echo '")
		cmd.WriteString(actionMarker)
		cmd.WriteString("'$?; ")
	}
	cmd.WriteString("tmux list-sessions -F '#{session_name}' 2>&1; echo '")
	cmd.WriteString(listMarker)
	cmd.WriteString("'$?")
	if validTermPattern.MatchString(term) {
		cmd.WriteString("; infocmp ")
		cmd.WriteString(shellQuote(term))
		cmd.WriteString(" >/dev/null 2>&1; echo '")
		cmd.WriteString(termMarker)
		cmd.WriteString("'$?")
	}

	out, sshErr := runSSH(configFile, hostName, cmd.String())

	probe := &HostProbe{}
	if !strings.Contains(out, listMarker) {
		// no marker: ssh never reached a shell
		reason := strings.TrimSpace(out)
		if reason == "" && sshErr != nil {
			reason = sshErr.Error()
		}
		probe.ListError = "could not list tmux sessions: " + reason
		return probe
	}

	p := parseProbe(out)
	probe.TermChecked = p.hasTerm
	probe.TermSupported = p.hasTerm && p.termRC == 0

	if p.hasAction && p.actionRC != 0 {
		probe.ActionError = strings.TrimSpace(p.actionText)
		if probe.ActionError == "" {
			probe.ActionError = "tmux command failed"
		}
	}

	if p.listRC == 0 {
		probe.Sessions = parseSessions(p.listText)
	} else if !isNoServerRunning(p.listText) {
		probe.ListError = "could not list tmux sessions: " + strings.TrimSpace(p.listText)
	}
	return probe
}

// probeParse holds the raw sections extracted from combined probe output.
type probeParse struct {
	actionText string
	actionRC   int
	hasAction  bool
	listText   string
	listRC     int
	termRC     int
	hasTerm    bool
}

// parseProbe splits combined probe output at its marker lines.
func parseProbe(out string) probeParse {
	var p probeParse
	var head, actionLines, listLines []string
	section := 0 // 0: before the first marker, 1: list output, 2: after the list

	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, actionMarker):
			p.actionRC, _ = strconv.Atoi(line[len(actionMarker):])
			p.hasAction = true
			// The lines seen before the first marker are the action output
			actionLines = head
			section = 1
		case strings.HasPrefix(line, listMarker):
			p.listRC, _ = strconv.Atoi(line[len(listMarker):])
			if !p.hasAction {
				// No action ran: the leading lines are the list output
				listLines = head
			}
			section = 2
		case strings.HasPrefix(line, termMarker):
			p.termRC, _ = strconv.Atoi(line[len(termMarker):])
			p.hasTerm = true
		default:
			switch section {
			case 0:
				head = append(head, line)
			case 1:
				listLines = append(listLines, line)
			}
		}
	}

	p.actionText = strings.Join(actionLines, "\n")
	p.listText = strings.Join(listLines, "\n")
	return p
}

// baseArgs builds the leading ssh arguments honoring an optional config file.
func baseArgs(configFile string) []string {
	if configFile != "" {
		return []string{"-F", configFile}
	}
	return nil
}

// runSSH runs a non-interactive command on host, returning combined output.
// The command must be shell-quoted; BatchMode blocks password prompts.
func runSSH(configFile, hostName, command string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	args := append(baseArgs(configFile),
		"-o", "BatchMode=yes", "-o", "ConnectTimeout=5", hostName, command)

	out, err := exec.CommandContext(ctx, "ssh", args...).CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("timed out contacting %s", hostName)
	}
	return string(out), err
}

// isNoServerRunning reports whether output just means "no sessions yet".
func isNoServerRunning(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "no server running") ||
		strings.Contains(lower, "error connecting to")
}

// parseSessions extracts session names from `tmux list-sessions` output.
func parseSessions(output string) []string {
	var sessions []string
	for _, line := range strings.Split(output, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			sessions = append(sessions, line)
		}
	}
	return sessions
}
