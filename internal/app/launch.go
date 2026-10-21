package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/EmpirioLabs-AI/cli/internal/agents"
	"github.com/EmpirioLabs-AI/cli/internal/config"
)

// mergedEnv is the current environment with the plan applied: its values
// replace existing ones (case-insensitively on Windows) and Unset names go.
func mergedEnv(p agents.Plan) []string {
	drop := map[string]bool{}
	norm := func(k string) string {
		if runtime.GOOS == "windows" {
			return strings.ToUpper(k)
		}
		return k
	}
	for k := range p.Env {
		drop[norm(k)] = true
	}
	for _, k := range p.Unset {
		drop[norm(k)] = true
	}
	var out []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if k == "" || !drop[norm(k)] {
			out = append(out, kv)
		}
	}
	for _, k := range sortedKeys(p.Env) {
		out = append(out, k+"="+p.Env[k])
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// runDir is where a session's temporary files live: inside the CLI's own
// directory, readable only by the user.
func runDir() (string, error) {
	dir := filepath.Join(config.Dir(), "run")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// writeFiles writes the plan's session files and returns a function that removes them.
func writeFiles(files []agents.File) (func(), error) {
	var written []string
	cleanup := func() {
		for _, f := range written {
			os.Remove(f)
		}
	}
	for _, f := range files {
		if err := config.WriteFileAtomic(f.Path, f.Data, 0o600); err != nil {
			cleanup()
			return func() {}, err
		}
		written = append(written, f.Path)
	}
	return cleanup, nil
}

// sweepRunDir removes session files left by a session that was killed.
func sweepRunDir(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || e.IsDir() {
			continue
		}
		if age := timeSince(info.ModTime()); age > staleSessionFile {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// launch runs the agent as a child process and waits for it, so the guards
// can put back what the agent saved during the session. It returns the
// agent's exit code.
func launch(p agents.Plan) (int, error) {
	for _, f := range p.Persist {
		if err := config.WriteFileAtomic(f.Path, f.Data, 0o600); err != nil {
			return 1, err
		}
	}
	cleanup, err := writeFiles(p.Files)
	if err != nil {
		return 1, err
	}
	defer cleanup()
	for _, g := range p.Guards {
		if err := g.Snapshot(); err != nil {
			// Without a snapshot a restore could be wrong, so do not start.
			return 1, fmt.Errorf("cannot read %s: %w", g.Describe(), err)
		}
	}
	defer func() {
		for _, g := range p.Guards {
			changed, err := g.Restore()
			switch {
			case err != nil:
				warnf("could not restore %s: %v", g.Describe(), err)
			case changed:
				notef("restored %s, which changed during the session", g.Describe())
			}
		}
	}()

	// The terminal sends Ctrl+C to the agent directly, since it shares our
	// process group; empirio only has to outlive it.
	signal.Ignore(os.Interrupt)
	defer signal.Reset(os.Interrupt)
	forward := forwardSignals()
	defer forward.stop()

	for {
		cmd, err := agentCommand(context.Background(), p.Binary, p.Args)
		if err != nil {
			return 1, err
		}
		cmd.Env = mergedEnv(p)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Start(); err != nil {
			return 1, err
		}
		forward.attach(cmd.Process)
		waitErr := cmd.Wait()
		code := 0
		if waitErr != nil {
			var exitErr *exec.ExitError
			if !errors.As(waitErr, &exitErr) {
				return 1, waitErr
			}
			code = exitCode(exitErr)
		}
		if !contains(p.Relaunch, code) {
			return code, nil
		}
	}
}

func contains(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// render prints a plan as shell commands with secrets masked.
func render(p agents.Plan) string {
	hide := func(s string) string {
		for _, secret := range p.Secrets {
			if secret != "" {
				s = strings.ReplaceAll(s, secret, config.Mask(secret))
			}
		}
		return s
	}
	var b strings.Builder
	windows := runtime.GOOS == "windows"
	for _, k := range p.Unset {
		if windows {
			fmt.Fprintf(&b, "Remove-Item Env:%s -ErrorAction SilentlyContinue\n", k)
		} else {
			fmt.Fprintf(&b, "unset %s\n", k)
		}
	}
	for _, k := range sortedKeys(p.Env) {
		if windows {
			fmt.Fprintf(&b, "$env:%s = %s\n", k, psQuote(hide(p.Env[k])))
		} else {
			fmt.Fprintf(&b, "export %s=%s\n", k, shQuote(hide(p.Env[k])))
		}
	}
	for _, f := range p.Files {
		fmt.Fprintf(&b, "# session file (mode 0600, removed after the session): %s\n", f.Path)
	}
	for _, f := range p.Persist {
		fmt.Fprintf(&b, "# launcher file (rewritten on every launch, holds no key): %s\n", f.Path)
	}
	if windows {
		b.WriteString("& " + psQuote(p.Binary))
	} else {
		b.WriteString(shQuote(p.Binary))
	}
	for _, a := range p.Args {
		if windows {
			b.WriteString(" " + psQuote(hide(a)))
		} else {
			b.WriteString(" " + shQuote(hide(a)))
		}
	}
	b.WriteString("\n")
	for _, g := range p.Guards {
		fmt.Fprintf(&b, "# after the session empirio restores: %s\n", g.Describe())
	}
	return b.String()
}

func shQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`!*?[]{}()<>|&;#~=,") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func psQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"$`!*?[]{}()<>|&;#@,") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
