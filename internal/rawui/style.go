package rawui

import (
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"golang.org/x/term"
)

const (
	localInfoColor    = "#22D3EE"
	localSuccessColor = "#4ADE80"
	localWarningColor = "#FACC15"
	localErrorColor   = "#F87171"
)

// Local UI output uses stderr, including when wrapped by the session logger.
func localColorsEnabled() bool {
	return os.Getenv("NO_COLOR") == "" && !strings.EqualFold(os.Getenv("TERM"), "dumb") && term.IsTerminal(int(os.Stderr.Fd()))
}

func localColor(text, color string) string {
	if !localColorsEnabled() {
		return text
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(text)
}
