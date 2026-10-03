package tmux

import (
	"strings"
	"testing"
)

func TestValidateSessionName(t *testing.T) {
	tests := []struct {
		name      string
		session   string
		wantValid bool
	}{
		{"simple word", "dev", true},
		{"with digits", "session2", true},
		{"with dash", "my-session", true},
		{"with underscore", "my_session", true},
		{"empty", "", false},
		{"whitespace only", "   ", false},
		{"with space", "my session", false},
		{"with dot", "my.session", false},
		{"with colon", "session:0", false},
		{"with shell metachar", "x;rm -rf /", false},
		{"with shell metachar quote", "x'$(boom)", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSessionName(tt.session)
			if tt.wantValid && err != nil {
				t.Errorf("ValidateSessionName(%q) unexpected error: %v", tt.session, err)
			}
			if !tt.wantValid && err == nil {
				t.Errorf("ValidateSessionName(%q) expected an error", tt.session)
			}
		})
	}
}

func TestShellQuote(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"dev", "'dev'"},
		{"my session", "'my session'"},
		{"it's", "'it'\\''s'"},
		{"a';rm -rf /", "'a'\\'';rm -rf /'"},
	}

	for _, tt := range tests {
		if got := shellQuote(tt.in); got != tt.want {
			t.Errorf("shellQuote(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSessionCommands(t *testing.T) {
	cmd, err := NewSessionCommand("dev")
	if err != nil {
		t.Fatalf("NewSessionCommand unexpected error: %v", err)
	}
	if cmd != "tmux new-session -d -s 'dev'" {
		t.Errorf("NewSessionCommand = %q", cmd)
	}

	// Invalid names are rejected before any SSH round-trip
	if _, err := NewSessionCommand("bad name"); err == nil {
		t.Error("Expected invalid session name to be rejected")
	}

	// Kill accepts any name coming from the host's session list
	if got := KillSessionCommand("it's odd"); got != "tmux kill-session -t 'it'\\''s odd'" {
		t.Errorf("KillSessionCommand = %q", got)
	}
}

func TestParseSessions(t *testing.T) {
	tests := []struct {
		name         string
		output       string
		wantSessions []string
	}{
		{"multiple sessions", "dev\neditor\nmain\n", []string{"dev", "editor", "main"}},
		{"single session", "solo\n", []string{"solo"}},
		{"empty output", "", nil},
		{"whitespace lines", "  dev \n\n  editor  \n", []string{"dev", "editor"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseSessions(tt.output)
			if len(got) != len(tt.wantSessions) {
				t.Fatalf("parseSessions(%q) = %v, want %v", tt.output, got, tt.wantSessions)
			}
			for i := range got {
				if got[i] != tt.wantSessions[i] {
					t.Errorf("parseSessions(%q)[%d] = %q, want %q", tt.output, i, got[i], tt.wantSessions[i])
				}
			}
		})
	}
}

func TestParseProbe(t *testing.T) {
	// Action failure followed by a successful list and a missing terminfo entry
	out := "duplicate session: dev\n" +
		"sshm.action:1\n" +
		"work\n" +
		"sshm.list:0\n" +
		"sshm.term:1\n"

	p := parseProbe(out)
	if !p.hasAction || p.actionRC != 1 || strings.TrimSpace(p.actionText) != "duplicate session: dev" {
		t.Errorf("action section misparsed: %+v", p)
	}
	if p.listRC != 0 || strings.TrimSpace(p.listText) != "work" {
		t.Errorf("list section misparsed: %+v", p)
	}
	if !p.hasTerm || p.termRC != 1 {
		t.Errorf("term section misparsed: %+v", p)
	}

	// No action, no term probe
	p = parseProbe("work\nsshm.list:0\n")
	if p.hasAction || p.hasTerm || p.listRC != 0 {
		t.Errorf("minimal probe misparsed: %+v", p)
	}
}

func TestIsNoServerRunning(t *testing.T) {
	tests := []struct {
		output string
		want   bool
	}{
		{"no server running on /tmp/tmux-1000/default", true},
		{"error connecting to /tmp/tmux-1000/default (No such file or directory)", true},
		{"ERROR CONNECTING TO /tmp/tmux-1000/default", true}, // case-insensitive check
		{"SomeOther: error connecting", false},               // partial match must not count
		{"Session dev created", false},
		{"", false},
	}

	for _, tt := range tests {
		if got := isNoServerRunning(tt.output); got != tt.want {
			t.Errorf("isNoServerRunning(%q) = %v, want %v", tt.output, got, tt.want)
		}
	}
}

func TestAttachCommandArgs(t *testing.T) {
	tests := []struct {
		name       string
		configFile string
		host       string
		session    string
		want       []string
	}{
		{
			name:    "default config",
			host:    "server1",
			session: "dev",
			want:    []string{"-t", "server1", "tmux", "new-session", "-A", "-s", "dev"},
		},
		{
			name:       "custom config file",
			configFile: "/custom/ssh_config",
			host:       "server2",
			session:    "editor",
			want:       []string{"-F", "/custom/ssh_config", "-t", "server2", "tmux", "new-session", "-A", "-s", "editor"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AttachCommandArgs(tt.configFile, tt.host, tt.session)
			if strings.Join(got, " ") != strings.Join(tt.want, " ") {
				t.Errorf("AttachCommandArgs() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBaseArgs(t *testing.T) {
	if got := baseArgs(""); got != nil {
		t.Errorf("baseArgs(\"\") = %v, want nil", got)
	}
	if got := baseArgs("/cfg"); len(got) != 2 || got[0] != "-F" || got[1] != "/cfg" {
		t.Errorf("baseArgs(\"/cfg\") = %v, want [-F /cfg]", got)
	}
}
