package ui

import (
	"fmt"
	"github.com/Gu1llaum-3/sshm/internal/connectivity"
	"strings"
	"time"
)

// formatTimeAgo formats a time into a readable "X time ago" string
func formatTimeAgo(t time.Time) string {
	now := time.Now()
	duration := now.Sub(t)

	switch {
	case duration < time.Minute:
		seconds := int(duration.Seconds())
		if seconds <= 1 {
			return "1 second ago"
		}
		return fmt.Sprintf("%d seconds ago", seconds)
	case duration < time.Hour:
		minutes := int(duration.Minutes())
		if minutes == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", minutes)
	case duration < 24*time.Hour:
		hours := int(duration.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	case duration < 7*24*time.Hour:
		days := int(duration.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	case duration < 30*24*time.Hour:
		weeks := int(duration.Hours() / (24 * 7))
		if weeks == 1 {
			return "1 week ago"
		}
		return fmt.Sprintf("%d weeks ago", weeks)
	case duration < 365*24*time.Hour:
		months := int(duration.Hours() / (24 * 30))
		if months == 1 {
			return "1 month ago"
		}
		return fmt.Sprintf("%d months ago", months)
	default:
		years := int(duration.Hours() / (24 * 365))
		if years == 1 {
			return "1 year ago"
		}
		return fmt.Sprintf("%d years ago", years)
	}
}

// formatConfigFile formats a config file path for display
func formatConfigFile(filePath string) string {
	if filePath == "" {
		return "Unknown"
	}
	// Show just the filename and parent directory for readability
	parts := strings.Split(filePath, "/")
	if len(parts) >= 2 {
		return fmt.Sprintf(".../%s/%s", parts[len(parts)-2], parts[len(parts)-1])
	}
	return filePath
}

// withTerm returns env with TERM replaced by term.
func withTerm(env []string, term string) []string {
	entry := "TERM=" + term
	for i, e := range env {
		if strings.HasPrefix(e, "TERM=") {
			env[i] = entry
			return env
		}
	}
	return append(env, entry)
}

// formatPingLatency renders a ping status and duration for the latency column
func formatPingLatency(status connectivity.PingStatus, duration time.Duration) string {
	switch status {
	case connectivity.StatusOnline:
		return fmt.Sprintf("%dms", duration.Milliseconds())
	case connectivity.StatusOffline:
		return "down"
	case connectivity.StatusConnecting:
		return "..."
	default:
		return ""
	}
}

// getPingLatencyString returns the latency of the last ping for a host
func (m *Model) getPingLatencyString(hostName string) string {
	if m.pingManager == nil {
		return ""
	}
	result, exists := m.pingManager.GetResult(hostName)
	if !exists {
		return ""
	}
	return formatPingLatency(result.Status, result.Duration)
}

// getPingStatusIndicator returns a colored circle indicator based on ping status
func (m *Model) getPingStatusIndicator(hostName string) string {
	if m.pingManager == nil {
		return "⚫" // Gray circle for unknown
	}

	status := m.pingManager.GetStatus(hostName)
	switch status {
	case connectivity.StatusOnline:
		return "🟢" // Green circle for online
	case connectivity.StatusOffline:
		return "🔴" // Red circle for offline
	case connectivity.StatusConnecting:
		return "🟡" // Yellow circle for connecting
	default:
		return "⚫" // Gray circle for unknown
	}
}

// extractHostNameFromTableRow extracts the host name from the first column,
// removing the ping status indicator
func extractHostNameFromTableRow(firstColumn string) string {
	// The first column format is: "🟢 hostname" or "⚫ hostname" etc.
	// We need to remove the emoji and space to get just the hostname
	parts := strings.Fields(firstColumn)
	if len(parts) >= 2 {
		// Return everything after the first part (the emoji)
		return strings.Join(parts[1:], " ")
	}
	// Fallback: if there's no space, return the whole string
	return firstColumn
}
