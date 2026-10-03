package ui

import (
	"os"
	"strings"

	"github.com/Gu1llaum-3/sshm/internal/tmux"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// mainSessionChoice is the entry for a plain SSH connection (no tmux).
const mainSessionChoice = "main"

// sessionChoice is one selectable entry of the connection window.
type sessionChoice struct {
	name   string
	isTmux bool
}

// sessionModel is the connection window: direct SSH or a tmux session.
type sessionModel struct {
	hostName      string
	configFile    string
	styles        Styles
	width         int
	height        int
	choices       []sessionChoice
	cursor        int
	loading       bool // true while the host is being queried
	err           string
	addMode       bool // true when typing a new tmux session name
	input         textinput.Model
	confirmDelete bool // true when waiting for confirmation of session removal
	termFallback  bool // true when the host lacks the local terminal description
}

// sessionsLoadedMsg carries the probe result for a host.
type sessionsLoadedMsg struct {
	hostName string
	probe    *tmux.HostProbe
}

// sessionConnectMsg asks the parent to open the SSH connection.
type sessionConnectMsg struct {
	hostName     string
	sshArgs      []string
	termFallback bool // run ssh with TERM=xterm-256color (host lacks our TERM)
}

// sessionCloseMsg is sent when the connection window is closed.
type sessionCloseMsg struct{}

// NewSessionForm creates a new connection window model for the given host
func NewSessionForm(hostName string, styles Styles, width, height int, configFile string) *sessionModel {
	input := textinput.New()
	input.Placeholder = "session-name"
	input.CharLimit = 30
	input.Width = 30

	return &sessionModel{
		hostName:   hostName,
		configFile: configFile,
		styles:     styles,
		width:      width,
		height:     height,
		choices:    []sessionChoice{{name: mainSessionChoice}},
		loading:    true,
		input:      input,
	}
}

func (m *sessionModel) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, probeHostCmd(m.hostName, m.configFile, ""))
}

// probeHostCmd probes a host, optionally running a tmux action first.
func probeHostCmd(hostName, configFile, action string) tea.Cmd {
	term := os.Getenv("TERM")
	return func() tea.Msg {
		return sessionsLoadedMsg{
			hostName: hostName,
			probe:    tmux.ProbeHost(hostName, configFile, action, term),
		}
	}
}

// tmuxChoices converts session names to selectable entries.
func tmuxChoices(sessions []string) []sessionChoice {
	choices := make([]sessionChoice, 0, len(sessions))
	for _, session := range sessions {
		choices = append(choices, sessionChoice{name: session, isTmux: true})
	}
	return choices
}

func (m *sessionModel) Update(msg tea.Msg) (*sessionModel, tea.Cmd) {
	switch msg := msg.(type) {
	case sessionsLoadedMsg:
		if msg.hostName != m.hostName {
			return m, nil
		}
		m.loading = false
		m.err = ""
		if probe := msg.probe; probe != nil {
			if probe.ActionError != "" {
				m.err = probe.ActionError
			} else if probe.ListError != "" {
				m.err = probe.ListError
			}
			m.termFallback = probe.TermChecked && !probe.TermSupported
			// "main" stays selectable even when the session list fails
			m.choices = append([]sessionChoice{{name: mainSessionChoice}},
				tmuxChoices(probe.Sessions)...)
		}
		if m.cursor >= len(m.choices) {
			m.cursor = len(m.choices) - 1
		}
		return m, nil

	case tea.KeyMsg:
		if m.addMode {
			return m.updateAddMode(msg)
		}

		switch msg.String() {
		case "ctrl+c", "esc":
			if m.confirmDelete {
				m.confirmDelete = false
				return m, nil
			}
			return m, func() tea.Msg { return sessionCloseMsg{} }

		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
			m.confirmDelete = false

		case "down", "j":
			if m.cursor < len(m.choices)-1 {
				m.cursor++
			}
			m.confirmDelete = false

		case "a":
			m.addMode = true
			m.err = ""
			m.confirmDelete = false
			m.input.SetValue("")
			m.input.Focus()
			return m, textinput.Blink

		case "x", "d":
			if m.cursor < len(m.choices) && m.choices[m.cursor].isTmux {
				// Pressing x again cancels the pending removal
				m.confirmDelete = !m.confirmDelete
			}

		case "enter":
			if m.cursor >= len(m.choices) {
				return m, nil
			}
			choice := m.choices[m.cursor]

			if m.confirmDelete && choice.isTmux {
				// kill the session and refresh the list
				m.confirmDelete = false
				m.loading = true
				return m, probeHostCmd(m.hostName, m.configFile,
					tmux.KillSessionCommand(choice.name))
			}

			var sshArgs []string
			if choice.isTmux {
				sshArgs = tmux.AttachCommandArgs(m.configFile, m.hostName, choice.name)
			} else {
				sshArgs = append(sshArgs, m.configArgs(m.hostName)...)
			}
			termFallback := m.termFallback && choice.isTmux
			return m, func() tea.Msg {
				return sessionConnectMsg{hostName: m.hostName, sshArgs: sshArgs, termFallback: termFallback}
			}
		}
	}
	return m, nil
}

// updateAddMode handles keys while typing a new tmux session name
func (m *sessionModel) updateAddMode(msg tea.KeyMsg) (*sessionModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.addMode = false
		m.input.SetValue("")
		m.input.Blur()
		m.err = ""
		return m, nil

	case "enter":
		command, err := tmux.NewSessionCommand(strings.TrimSpace(m.input.Value()))
		if err != nil {
			m.err = err.Error()
			return m, nil
		}
		m.addMode = false
		m.input.SetValue("")
		m.input.Blur()
		m.err = ""
		m.loading = true
		return m, probeHostCmd(m.hostName, m.configFile, command)
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// configArgs returns the ssh arguments for a plain connection to host
func (m *sessionModel) configArgs(hostName string) []string {
	if m.configFile != "" {
		return []string{"-F", m.configFile, hostName}
	}
	return []string{hostName}
}

func (m *sessionModel) View() string {
	var lines []string

	lines = append(lines, m.styles.FormTitle.Render("Connect to "+m.hostName))
	lines = append(lines, "")

	for i, choice := range m.choices {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}
		label := choice.name + "  (direct SSH)"
		if choice.isTmux {
			label = "tmux: " + choice.name
		}
		if i == m.cursor {
			lines = append(lines, m.styles.FocusedLabel.Render(cursor+label))
		} else {
			lines = append(lines, m.styles.HelpText.Render(cursor+label))
		}
	}

	if m.loading {
		lines = append(lines, "")
		lines = append(lines, m.styles.SortInfo.Render("Loading tmux sessions..."))
	}

	if m.addMode {
		lines = append(lines, "")
		lines = append(lines, m.styles.FocusedLabel.Render("New tmux session name:"))
		lines = append(lines, m.input.View())
		lines = append(lines, m.styles.HelpText.Render("Enter: create • Esc: cancel"))
	}

	if m.confirmDelete && m.cursor < len(m.choices) && m.choices[m.cursor].isTmux {
		lines = append(lines, "")
		lines = append(lines, m.styles.ErrorText.Render(
			"Remove tmux session '"+m.choices[m.cursor].name+"'? Enter: confirm • Esc: cancel"))
	}

	if m.err != "" {
		lines = append(lines, "")
		lines = append(lines, m.styles.ErrorText.Render("Error: "+m.err))
	}

	lines = append(lines, "")
	lines = append(lines, m.styles.HelpText.Render(
		" ↑/↓: select • Enter: connect • a: add tmux session • x: remove tmux session • Esc: close"))

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)

	return lipgloss.Place(
		m.width,
		m.height,
		lipgloss.Center,
		lipgloss.Center,
		m.styles.FormContainer.Render(content),
	)
}
