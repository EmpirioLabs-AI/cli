//go:build windows

package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

// agentCommand returns the command that runs binary with args.
//
// npm and pnpm install an agent as a .cmd launcher, which only cmd.exe can run,
// and cmd.exe splits a launcher path that contains a space (C:\Users\Jane Doe\...)
// and expands %, ! and ^ in arguments. A launcher that wraps a Node script is
// therefore run as that script, without cmd.exe, which also spares the
// "Terminate batch job (Y/N)?" question on Ctrl+C. Any other batch file runs
// through cmd.exe with every argument quoted, and an argument a batch file
// cannot pass on unchanged is refused rather than altered.
func agentCommand(ctx context.Context, binary string, args []string) (*exec.Cmd, error) {
	ext := strings.ToLower(filepath.Ext(binary))
	if ext != ".cmd" && ext != ".bat" {
		return exec.CommandContext(ctx, binary, args...), nil
	}
	if node, prefix, ok := nodeLauncher(binary); ok {
		return exec.CommandContext(ctx, node, append(prefix, args...)...), nil
	}
	return batchCommand(ctx, binary, args)
}

// The script a launcher runs: a quoted path relative to the launcher's own
// directory (%dp0% in npm's launchers, %~dp0 in pnpm's), naming a .js file.
var launcherScript = regexp.MustCompile(`(?i)"%(?:~dp0|dp0%)\\*([^"%]+?\.[cm]?js)"`)

// launcherNode is the program token before the script: npm's "%_prog%",
// pnpm's "%~dp0\node.exe", or a bare node.
var launcherNode = regexp.MustCompile(`(?i)^(?:"%_prog%"|"%(?:~dp0|dp0%)\\*node(?:\.exe)?"|node(?:\.exe)?)$`)

// nodeLauncher reads an npm or pnpm launcher and returns the node program and
// the arguments before the user's: node's own flags, then the script.
func nodeLauncher(path string) (node string, prefix []string, ok bool) {
	info, err := os.Stat(path)
	if err != nil || info.Size() > 64<<10 {
		return "", nil, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", nil, false
	}
	dir := filepath.Dir(path)
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		loc := launcherScript.FindStringSubmatchIndex(line)
		if loc == nil || !strings.Contains(line[loc[1]:], "%*") {
			continue
		}
		script := filepath.Join(dir, line[loc[2]:loc[3]])
		if _, err := os.Stat(script); err != nil {
			return "", nil, false
		}
		// Walk back from the script to the program, keeping node's flags.
		before := strings.Fields(line[:loc[0]])
		var flags []string
		found := false
		for i := len(before) - 1; i >= 0; i-- {
			tok := strings.TrimPrefix(before[i], "@")
			if launcherNode.MatchString(tok) {
				found = true
				break
			}
			if !strings.HasPrefix(tok, "-") || strings.ContainsAny(tok, `"%!^&|<>`) {
				return "", nil, false
			}
			flags = append([]string{tok}, flags...)
		}
		if !found {
			return "", nil, false
		}
		node = filepath.Join(dir, "node.exe")
		if _, err := os.Stat(node); err != nil {
			if node, err = exec.LookPath("node"); err != nil {
				return "", nil, false
			}
		}
		return node, append(flags, script), true
	}
	return "", nil, false
}

// batchCommand runs a batch file through cmd.exe with each argument quoted.
func batchCommand(ctx context.Context, binary string, args []string) (*exec.Cmd, error) {
	const unsafe = "\"%!^\r\n"
	if strings.ContainsAny(binary, unsafe) {
		return nil, fmt.Errorf("cannot run %s: its path contains a character cmd.exe would change", binary)
	}
	var b strings.Builder
	b.WriteString(`/d /s /c ""` + binary + `"`)
	for _, a := range args {
		if strings.ContainsAny(a, unsafe) {
			return nil, fmt.Errorf("%s is a batch file, which cannot receive the argument %q unchanged (it contains \", %%, !, ^ or a line break); run it without empirio, or install the agent another way", filepath.Base(binary), a)
		}
		b.WriteString(" " + quoteArg(a))
	}
	b.WriteString(`"`)
	comspec := os.Getenv("ComSpec")
	if comspec == "" {
		comspec = filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	}
	if !filepath.IsAbs(comspec) {
		return nil, errors.New("ComSpec does not name cmd.exe by its full path")
	}
	cmd := exec.CommandContext(ctx, comspec)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: syscall.EscapeArg(comspec) + " " + b.String()}
	return cmd, nil
}

// quoteArg quotes an argument the way programs on Windows read their command
// line: backslashes double only before a quote, here only the closing one.
func quoteArg(s string) string {
	trailing := len(s) - len(strings.TrimRight(s, `\`))
	return `"` + s + strings.Repeat(`\`, trailing) + `"`
}
