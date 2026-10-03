package ui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Gu1llaum-3/sshm/internal/connectivity"
	"github.com/Gu1llaum-3/sshm/internal/tmux"
	tea "github.com/charmbracelet/bubbletea"
)

// createTestSessionForm creates a session window with main + two tmux sessions
func createTestSessionForm() *sessionModel {
	m := NewSessionForm("server1", NewStyles(80), 80, 24, "")
	// Simulate the async probe result arriving
	newForm, _ := m.Update(sessionsLoadedMsg{
		hostName: "server1",
		probe:    &tmux.HostProbe{Sessions: []string{"dev", "editor"}},
	})
	return newForm
}

func TestEnterOpensConnectionWindow(t *testing.T) {
	m := createTestModel()

	// Press Enter on the selected host
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = newModel.(Model)

	if m.viewMode != ViewSessionSelect {
		t.Errorf("Expected ViewSessionSelect after Enter, got %v", m.viewMode)
	}
	if m.sessionForm == nil {
		t.Fatal("Expected sessionForm to be created after Enter")
	}
	if m.sessionForm.hostName != "server1" {
		t.Errorf("Expected session form for 'server1', got '%s'", m.sessionForm.hostName)
	}
}

func TestSessionsLoadedPopulatesChoices(t *testing.T) {
	m := createTestSessionForm()

	// Choices: main + dev + editor
	if len(m.choices) != 3 {
		t.Fatalf("Expected 3 choices, got %d", len(m.choices))
	}
	if m.choices[0].name != mainSessionChoice || m.choices[0].isTmux {
		t.Errorf("Expected first choice to be main (non-tmux), got %+v", m.choices[0])
	}
	if !m.choices[1].isTmux || m.choices[1].name != "dev" {
		t.Errorf("Expected second choice to be tmux 'dev', got %+v", m.choices[1])
	}
	if m.loading {
		t.Error("Form should not be loading after sessions are loaded")
	}
}

func TestSessionLoadedForOtherHostIgnored(t *testing.T) {
	m := createTestSessionForm()

	newForm, _ := m.Update(sessionsLoadedMsg{
		hostName: "other-host",
		probe:    &tmux.HostProbe{Sessions: []string{"x"}},
	})
	if len(newForm.choices) != 3 {
		t.Errorf("Result for another host must be ignored, got %d choices", len(newForm.choices))
	}
}

func TestSessionLoadedWithActionErrorStillListsSessions(t *testing.T) {
	m := NewSessionForm("server1", NewStyles(80), 80, 24, "")

	newForm, _ := m.Update(sessionsLoadedMsg{
		hostName: "server1",
		probe: &tmux.HostProbe{
			Sessions:    []string{"dev"},
			ActionError: "duplicate session: dev",
		},
	})

	if newForm.err != "duplicate session: dev" {
		t.Errorf("Expected the action error to be shown, got %q", newForm.err)
	}
	// The list from the same round-trip must still populate the choices
	if len(newForm.choices) != 2 || !newForm.choices[1].isTmux || newForm.choices[1].name != "dev" {
		t.Errorf("Expected main + tmux dev, got %+v", newForm.choices)
	}
}

func TestSessionNavigation(t *testing.T) {
	m := createTestSessionForm()

	// Move down twice then up once
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.cursor != 1 {
		t.Errorf("Expected cursor at 1, got %d", m.cursor)
	}

	// Cursor must clamp at the last entry
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.cursor != len(m.choices)-1 {
		t.Errorf("Expected cursor clamped at %d, got %d", len(m.choices)-1, m.cursor)
	}

	// Cursor must clamp at the first entry
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.cursor != 0 {
		t.Errorf("Expected cursor clamped at 0, got %d", m.cursor)
	}
}

func TestConnectViaMainReturnsPlainSSHArgs(t *testing.T) {
	m := createTestSessionForm()

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Expected a command on Enter")
	}
	msg := cmd()
	connectMsg, ok := msg.(sessionConnectMsg)
	if !ok {
		t.Fatalf("Expected sessionConnectMsg, got %T", msg)
	}
	if connectMsg.hostName != "server1" {
		t.Errorf("Expected hostName 'server1', got '%s'", connectMsg.hostName)
	}
	want := []string{"server1"}
	if !reflect.DeepEqual(connectMsg.sshArgs, want) {
		t.Errorf("Expected ssh args %v, got %v", want, connectMsg.sshArgs)
	}
}

func TestConnectViaTmuxReturnsAttachArgs(t *testing.T) {
	m := createTestSessionForm()

	// Select the first tmux session ("dev") then press Enter
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Expected a command on Enter")
	}
	msg := cmd()
	connectMsg, ok := msg.(sessionConnectMsg)
	if !ok {
		t.Fatalf("Expected sessionConnectMsg, got %T", msg)
	}
	want := []string{"-t", "server1", "tmux", "new-session", "-A", "-s", "dev"}
	if !reflect.DeepEqual(connectMsg.sshArgs, want) {
		t.Errorf("Expected ssh args %v, got %v", want, connectMsg.sshArgs)
	}
}

func TestConnectViaTmuxWithConfigFile(t *testing.T) {
	m := NewSessionForm("server1", NewStyles(80), 80, 24, "/custom/ssh_config")
	m, _ = m.Update(sessionsLoadedMsg{
		hostName: "server1",
		probe:    &tmux.HostProbe{Sessions: []string{"dev"}},
	})

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	msg := cmd()
	connectMsg, ok := msg.(sessionConnectMsg)
	if !ok {
		t.Fatalf("Expected sessionConnectMsg, got %T", msg)
	}
	want := []string{"-F", "/custom/ssh_config", "-t", "server1", "tmux", "new-session", "-A", "-s", "dev"}
	if !reflect.DeepEqual(connectMsg.sshArgs, want) {
		t.Errorf("Expected ssh args %v, got %v", want, connectMsg.sshArgs)
	}
}

func TestConnectViaTmuxFallsBackTermWhenHostLacksIt(t *testing.T) {
	m := NewSessionForm("server1", NewStyles(80), 80, 24, "")
	// Simulate the probe result: the host does not know our terminal (e.g. xterm-ghostty)
	m, _ = m.Update(sessionsLoadedMsg{
		hostName: "server1",
		probe:    &tmux.HostProbe{Sessions: []string{"dev"}, TermChecked: true, TermSupported: false},
	})
	if !m.termFallback {
		t.Fatal("Expected termFallback to be recorded from the probe")
	}

	// Connecting to a tmux session must request the TERM fallback
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	connectMsg, ok := cmd().(sessionConnectMsg)
	if !ok {
		t.Fatalf("Expected sessionConnectMsg, got %T", cmd())
	}
	if !connectMsg.termFallback {
		t.Error("Expected termFallback on tmux connect when the host lacks the terminal description")
	}

	// Connecting via main must NOT downgrade the terminal
	m2 := NewSessionForm("server1", NewStyles(80), 80, 24, "")
	m2, _ = m2.Update(sessionsLoadedMsg{
		hostName: "server1",
		probe:    &tmux.HostProbe{Sessions: []string{"dev"}, TermChecked: true, TermSupported: false},
	})
	_, cmd2 := m2.Update(tea.KeyMsg{Type: tea.KeyEnter})
	connectMsg2, ok := cmd2().(sessionConnectMsg)
	if !ok {
		t.Fatalf("Expected sessionConnectMsg, got %T", cmd2())
	}
	if connectMsg2.termFallback {
		t.Error("main connection must keep the local terminal description")
	}
}

func TestAddSessionInvalidNameShowsError(t *testing.T) {
	m := createTestSessionForm()

	// Enter add mode, type an invalid name, submit
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if !m.addMode {
		t.Fatal("Expected add mode after pressing 'a'")
	}
	for _, ch := range "bad.name" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
	}
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if m.err == "" {
		t.Error("Expected a validation error for an invalid session name")
	}
	if cmd != nil {
		t.Error("Expected no command on invalid session name (no SSH call)")
	}
	if !m.addMode {
		t.Error("Should stay in add mode to allow correcting the name")
	}

	// Esc exits add mode without creating anything
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.addMode {
		t.Error("Expected add mode to be cancelled by Esc")
	}
}

func TestRemoveSessionRequiresConfirmation(t *testing.T) {
	m := createTestSessionForm()

	// Select the tmux session "dev" and press x
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if !m.confirmDelete {
		t.Fatal("Expected confirmation state after pressing 'x' on a tmux session")
	}

	// Pressing x again cancels
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if m.confirmDelete {
		t.Fatal("Expected confirmation to be cancelled by pressing 'x' again")
	}

	// Pressing x then Enter returns a command (the kill + reload); we only
	// check that it is produced, never execute it (it would call ssh).
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Expected a command confirming session removal")
	}
	if !m.loading {
		t.Error("Expected loading state while the session list reloads")
	}
}

func TestRemoveMainNotOffered(t *testing.T) {
	m := createTestSessionForm()

	// 'main' is selected (cursor 0); x must not offer removal
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if m.confirmDelete {
		t.Error("Removal must not be offered for the 'main' entry")
	}
}

func TestSessionCloseReturnsToList(t *testing.T) {
	m := createTestModel()

	// Open the connection window
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = newModel.(Model)

	// Close it with Esc: the form produces a close message that must be fed
	// back through the parent Update, as the tea runtime would do
	newModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = newModel.(Model)
	if cmd == nil {
		t.Fatal("Expected a close command on Esc")
	}
	if msg := cmd(); msg != nil {
		newModel, _ = m.Update(msg)
		m = newModel.(Model)
	}

	if m.viewMode != ViewList {
		t.Errorf("Expected ViewList after closing the connection window, got %v", m.viewMode)
	}
	if m.sessionForm != nil {
		t.Error("Expected sessionForm to be cleared")
	}
}

func TestFormatPingLatency(t *testing.T) {
	tests := []struct {
		name     string
		status   connectivity.PingStatus
		duration time.Duration
		want     string
	}{
		{"online", connectivity.StatusOnline, 150 * time.Millisecond, "150ms"},
		{"online sub-ms", connectivity.StatusOnline, 300 * time.Microsecond, "0ms"},
		{"offline", connectivity.StatusOffline, time.Second, "down"},
		{"connecting", connectivity.StatusConnecting, 0, "..."},
		{"unknown", connectivity.StatusUnknown, 0, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatPingLatency(tt.status, tt.duration); got != tt.want {
				t.Errorf("formatPingLatency(%v, %v) = %q, want %q", tt.status, tt.duration, got, tt.want)
			}
		})
	}
}

func TestPingLatencyColumn(t *testing.T) {
	m := createTestModel()

	// The table must expose a Ping column between Name and Hostname
	columns := m.table.Columns()
	if len(columns) != 5 {
		t.Fatalf("Expected 5 table columns, got %d", len(columns))
	}
	if columns[1].Title != "Ping" {
		t.Errorf("Expected 'Ping' column at index 1, got %q", columns[1].Title)
	}

	// Rows must have 5 cells with the latency cell at index 1 (empty when no ping ran)
	rows := m.table.Rows()
	if len(rows) == 0 {
		t.Fatal("Expected table rows")
	}
	for _, row := range rows {
		if len(row) != 5 {
			t.Fatalf("Expected 5 cells per row, got %d", len(row))
		}
		if row[1] != "" {
			t.Errorf("Expected empty latency cell before any ping, got %q", row[1])
		}
	}

	// Host name extraction must keep working with the new layout
	if got := extractHostNameFromTableRow(rows[0][0]); got != "server1" {
		t.Errorf("Expected 'server1' from first column, got %q", got)
	}
}

func TestWithTerm(t *testing.T) {
	env := []string{"PATH=/bin", "TERM=xterm-ghostty", "HOME=/home"}
	want := []string{"PATH=/bin", "TERM=xterm-256color", "HOME=/home"}
	if got := withTerm(env, "xterm-256color"); !reflect.DeepEqual(got, want) {
		t.Errorf("withTerm replace = %v, want %v", got, want)
	}

	env = []string{"PATH=/bin"}
	want = []string{"PATH=/bin", "TERM=xterm-256color"}
	if got := withTerm(env, "xterm-256color"); !reflect.DeepEqual(got, want) {
		t.Errorf("withTerm append = %v, want %v", got, want)
	}
}

func TestSessionViewRenders(t *testing.T) {
	m := createTestSessionForm()
	view := m.View()

	for _, want := range []string{"main", "tmux: dev", "tmux: editor"} {
		if !strings.Contains(view, want) {
			t.Errorf("Expected session view to contain %q", want)
		}
	}
}
