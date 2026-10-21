//go:build windows

package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const npmLauncher = "@ECHO off\r\nGOTO start\r\n:find_dp0\r\nSET dp0=%~dp0\r\nEXIT /b\r\n:start\r\nSETLOCAL\r\nCALL :find_dp0\r\n\r\n" +
	"IF EXIST \"%dp0%\\node.exe\" (\r\n  SET \"_prog=%dp0%\\node.exe\"\r\n) ELSE (\r\n  SET \"_prog=node\"\r\n  SET PATHEXT=%PATHEXT:;.JS;=;%\r\n)\r\n\r\n" +
	"endLocal & goto #_undefined_# 2>NUL || title %COMSPEC% & \"%_prog%\"  \"%dp0%\\node_modules\\@acme\\agent\\bin\\agent.js\" %*\r\n"

const pnpmLauncher = "@SETLOCAL\r\n@IF NOT DEFINED NODE_PATH (\r\n  @SET \"NODE_PATH=%~dp0\\..\\node_modules\"\r\n)\r\n" +
	"@IF EXIST \"%~dp0\\node.exe\" (\r\n  \"%~dp0\\node.exe\" --no-warnings \"%~dp0\\..\\@acme\\agent\\bin\\agent.mjs\" %*\r\n) ELSE (\r\n" +
	"  @SET PATHEXT=%PATHEXT:;.JS;=;%\r\n  node --no-warnings \"%~dp0\\..\\@acme\\agent\\bin\\agent.mjs\" %*\r\n)\r\n"

// The other kind of launcher: a batch file that hands its arguments to node.
const plainLauncher = "@echo off\r\n\"node\" \"%~dp0print.js\" %*\r\n"

const printArgs = "console.log(JSON.stringify(process.argv.slice(2)))"

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// home is a directory with a space in its name, as under C:\Users\Jane Doe.
func home(t *testing.T) string { return filepath.Join(t.TempDir(), "Jane Doe") }

func TestNpmLauncherRunsItsScript(t *testing.T) {
	dir := filepath.Join(home(t), "npm")
	write(t, filepath.Join(dir, `node_modules\@acme\agent\bin\agent.js`), printArgs)
	write(t, filepath.Join(dir, "agent.cmd"), npmLauncher)
	node, prefix, ok := nodeLauncher(filepath.Join(dir, "agent.cmd"))
	if !ok || node == "" || len(prefix) != 1 || !strings.HasSuffix(prefix[0], `@acme\agent\bin\agent.js`) {
		t.Fatalf("%q %q %v", node, prefix, ok)
	}
}

func TestPnpmLauncherKeepsNodeFlags(t *testing.T) {
	bin := filepath.Join(home(t), "pnpm", ".bin")
	write(t, filepath.Join(bin, `..\@acme\agent\bin\agent.mjs`), printArgs)
	write(t, filepath.Join(bin, "agent.cmd"), pnpmLauncher)
	_, prefix, ok := nodeLauncher(filepath.Join(bin, "agent.cmd"))
	if !ok || len(prefix) != 2 || prefix[0] != "--no-warnings" || !strings.HasSuffix(prefix[1], "agent.mjs") {
		t.Fatalf("%q %v", prefix, ok)
	}
}

func TestLauncherArgumentsArriveUnchanged(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	dir := filepath.Join(home(t), "npm")
	write(t, filepath.Join(dir, `node_modules\@acme\agent\bin\agent.js`), printArgs)
	write(t, filepath.Join(dir, "agent.cmd"), npmLauncher)
	args := []string{"-p", `say "hi" & echo 100% ^done! (now) | more`, `C:\dir\`, ""}
	cmd, err := agentCommand(context.Background(), filepath.Join(dir, "agent.cmd"), args)
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	want := `["-p","say \"hi\" & echo 100% ^done! (now) | more","C:\\dir\\",""]`
	if got := strings.TrimSpace(string(out)); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestOtherBatchFilesRunThroughCmdWithQuoting(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	dir := home(t)
	write(t, filepath.Join(dir, "print.js"), printArgs)
	path := filepath.Join(dir, "tool.cmd")
	write(t, path, plainLauncher)
	if _, _, ok := nodeLauncher(path); ok {
		t.Fatal("this launcher should take the cmd.exe path")
	}
	cmd, err := agentCommand(context.Background(), path, []string{"a b & c | d > e (f)", `g\`, "it's"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	want := `["a b & c | d > e (f)","g\\","it's"]`
	if got := strings.TrimSpace(string(out)); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	for _, bad := range []string{`say "hi"`, "100%", "wow!", "a^b", "two\nlines"} {
		if _, err := agentCommand(context.Background(), path, []string{bad}); err == nil {
			t.Errorf("%q was not refused", bad)
		}
	}
}
