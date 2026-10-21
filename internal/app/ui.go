package app

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// Output goes to stderr except for what a command exists to print (a model
// list, a plan), so the agent's own output and anything piped stay clean.
var (
	stdout io.Writer = os.Stdout
	stderr io.Writer = os.Stderr
)

func isTerminal(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }

func colorEnabled() bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	return isTerminal(os.Stderr)
}

var (
	brand  = lipgloss.Color("#0A7CFF")
	green  = lipgloss.Color("#22C55E")
	red    = lipgloss.Color("#EF4444")
	yellow = lipgloss.Color("#EAB308")
	gray   = lipgloss.Color("#8B949E")
)

func paint(s string, style lipgloss.Style) string {
	if !colorEnabled() {
		return s
	}
	return style.Render(s)
}

func bold(s string) string    { return paint(s, lipgloss.NewStyle().Bold(true)) }
func dim(s string) string     { return paint(s, lipgloss.NewStyle().Foreground(gray)) }
func accent(s string) string  { return paint(s, lipgloss.NewStyle().Foreground(brand).Bold(true)) }
func success(s string) string { return paint(s, lipgloss.NewStyle().Foreground(green)) }
func danger(s string) string  { return paint(s, lipgloss.NewStyle().Foreground(red)) }
func caution(s string) string { return paint(s, lipgloss.NewStyle().Foreground(yellow)) }

func okf(format string, args ...any) {
	fmt.Fprintln(stderr, success("✓")+" "+fmt.Sprintf(format, args...))
}

func failf(format string, args ...any) {
	fmt.Fprintln(stderr, danger("✗")+" "+fmt.Sprintf(format, args...))
}

func warnf(format string, args ...any) {
	fmt.Fprintln(stderr, caution("!")+" "+fmt.Sprintf(format, args...))
}

func notef(format string, args ...any) {
	fmt.Fprintln(stderr, dim("·")+" "+fmt.Sprintf(format, args...))
}

// readSecret reads a key without echoing it in a terminal, or a line from a pipe.
func readSecret(prompt string) (string, error) {
	if isTerminal(os.Stdin) {
		fmt.Fprint(stderr, prompt)
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(stderr)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// confirm asks a yes/no question in a terminal; the default is no.
func confirm(question string) bool {
	fmt.Fprint(stderr, question+" [y/N] ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

func money(amount float64) string { return fmt.Sprintf("$%.2f", amount) }

func ago(t time.Time) string {
	d := timeSince(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d.Minutes()), "minute") + " ago"
	case d < 48*time.Hour:
		return plural(int(d.Hours()), "hour") + " ago"
	default:
		return plural(int(d.Hours()/24), "day") + " ago"
	}
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

var timeSince = time.Since

const staleSessionFile = 24 * time.Hour
