// Package agents knows how to start each supported coding agent already
// connected to EmpirioLabs AI.
//
// An agent gets everything through its own process: environment variables,
// command-line flags, or a temporary settings file that only this session
// reads. Nothing is written to the agent's configuration, so running the agent
// directly afterwards behaves exactly as it did before.
package agents

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/EmpirioLabs-AI/cli/internal/catalog"
	"github.com/EmpirioLabs-AI/cli/internal/guard"
)

// Context is what an agent's plan is built from.
type Context struct {
	Key     string
	BaseURL string // API origin, no trailing slash, no /v1
	Main    catalog.Model
	Fast    catalog.Model
	Catalog *catalog.Catalog
	Version string
	RunDir  string // where this session's temporary files go
}

// File is written (mode 0600) before the agent starts and removed when it exits.
type File struct {
	Path string
	Data []byte
}

// Plan is everything needed to start one agent session.
type Plan struct {
	Binary  string
	Args    []string // after the binary
	Env     map[string]string
	Unset   []string // removed from the agent's environment
	Files   []File   // removed when the session ends
	Persist []File   // kept: launcher-owned files the agent reads across sessions
	Secrets []string // masked when the plan is printed
	Summary string
	Guards  []guard.Guard
	// Relaunch lists exit codes with which the agent asks to be started again
	// (Gemini CLI restarts itself after some settings change).
	Relaunch []int
}

// Agent is one supported coding agent.
type Agent struct {
	Name     string   // the command: empirio <name>
	Aliases  []string // other names the command accepts
	Title    string
	Binaries []string // executables to look for, in order
	Surface  catalog.Surface
	Install  map[string]string // install command by GOOS, "" for every OS
	Docs     string
	// Plan builds the launch; args are the user's own arguments for the agent.
	Plan func(c Context, binary string, args []string) (Plan, error)
}

// InstallHint is the command that installs the agent on this OS.
func (a Agent) InstallHint() string {
	if hint, ok := a.Install[runtime.GOOS]; ok {
		return hint
	}
	return a.Install[""]
}

// Find returns the installed executable, or "" when the agent is not installed.
func (a Agent) Find() string {
	for _, name := range a.Binaries {
		if p, err := exec.LookPath(name); err == nil {
			if abs, err := filepath.Abs(p); err == nil {
				return abs
			}
			return p
		}
	}
	return ""
}

var registry []Agent

func register(a Agent) { registry = append(registry, a) }

// All returns every supported agent, in menu order.
func All() []Agent {
	out := append([]Agent(nil), registry...)
	sort.SliceStable(out, func(i, j int) bool { return order(out[i].Name) < order(out[j].Name) })
	return out
}

var menuOrder = []string{"claude", "codex", "opencode", "gemini", "qwen", "crush", "kilo", "aider", "goose", "openhands"}

func order(name string) int {
	for i, n := range menuOrder {
		if n == name {
			return i
		}
	}
	return len(menuOrder)
}

// Get returns the agent a command name or alias names.
func Get(name string) (Agent, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, a := range registry {
		if a.Name == name {
			return a, true
		}
		for _, alias := range a.Aliases {
			if alias == name {
				return a, true
			}
		}
	}
	return Agent{}, false
}

// hasFlag reports whether args (before any "--") contain one of the flags.
func hasFlag(args []string, names ...string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		for _, n := range names {
			if a == n || strings.HasPrefix(a, n+"=") {
				return true
			}
		}
	}
	return false
}

// homeFile is <$envDir or ~/defaultDir>/name.
func homeFile(envDir, defaultDir, name string) string {
	if d := os.Getenv(envDir); d != "" {
		return filepath.Join(d, name)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, defaultDir, name)
}

// contextFor is the context window a session uses: the model's own, or a
// conservative default when the catalog does not say.
func contextFor(m catalog.Model) int {
	if m.ContextWindow > 0 {
		return m.ContextWindow
	}
	return 128_000
}

// compactAt is where long sessions summarize themselves: the model's window up
// to 200K tokens, so a 1M-token model does not resend a huge context every turn.
func compactAt(m catalog.Model) int {
	const limit = 200_000
	if c := contextFor(m); c < limit {
		return c
	}
	return limit
}

// outputFor caps the reply length an agent asks for on every request.
func outputFor(m catalog.Model, ceiling int) int {
	if m.MaxOutputTokens > 0 && m.MaxOutputTokens < ceiling {
		return m.MaxOutputTokens
	}
	return ceiling
}

func describe(m catalog.Model) string {
	parts := []string{m.ID}
	if m.ContextWindow > 0 {
		parts = append(parts, tokens(m.ContextWindow)+" context")
	}
	if p := m.Price(); p.Known {
		parts = append(parts, fmt.Sprintf("$%s in / $%s out per 1M", money(p.Input), money(p.Output)))
	}
	return strings.Join(parts, ", ")
}

func tokens(n int) string {
	switch {
	case n >= 1_000_000:
		s := strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1_000_000), ".0")
		// 1,048,576 and 1,024,000 are both sold as a 1M window.
		if f := float64(n) / 1_000_000; f-math.Floor(f) < 0.05 {
			s = fmt.Sprintf("%d", int(f))
		}
		return s + "M"
	case n >= 1_000:
		return fmt.Sprintf("%dK", (n+500)/1000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// money prints a per-million price the way the pricing page does.
func money(v float64) string {
	s := fmt.Sprintf("%.3f", v)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if !strings.Contains(s, ".") {
		return s + ".00"
	}
	if i := strings.Index(s, "."); len(s)-i-1 == 1 {
		return s + "0"
	}
	return s
}

// Tokens and Money are shared with the command layer.
func Tokens(n int) string             { return tokens(n) }
func Money(v float64) string          { return money(v) }
func Describe(m catalog.Model) string { return describe(m) }

// sessionID names this session's temporary files.
func sessionID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", os.Getpid())
	}
	return hex.EncodeToString(b)
}
