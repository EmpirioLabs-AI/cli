package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var installLabels = []struct{ key, label string }{{"default", ""}, {"darwin", "macOS"}, {"linux", "Linux"}, {"windows", "Windows"}}

// agentTable renders the README's agent table from the agent list.
func agentTable() string {
	cell := func(s string) string { return "`" + strings.ReplaceAll(s, "|", `\|`) + "`" }
	var b strings.Builder
	b.WriteString("| Agent | Command | EmpirioLabs AI tools | Install |\n|---|---|---|---|\n")
	for _, r := range agentRecords() {
		var install []string
		for _, l := range installLabels {
			if cmd, ok := r.Install[l.key]; ok {
				if l.label != "" {
					install = append(install, l.label+": "+cell(cmd))
				} else {
					install = append(install, cell(cmd))
				}
			}
		}
		tools := "no"
		if r.Tools {
			tools = "yes"
		}
		b.WriteString("| " + r.Title + " | `" + r.Command + "` | " + tools + " | " + strings.Join(install, "<br>") + " |\n")
	}
	return b.String()
}

// TestReadmeListsEveryAgent keeps the README's table in step with the agent
// list. UPDATE_README=1 rewrites it.
func TestReadmeListsEveryAgent(t *testing.T) {
	path := filepath.Join("..", "..", "README.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	readme := strings.ReplaceAll(string(raw), "\r\n", "\n")
	start := strings.Index(readme, "<!-- agents:start")
	end := strings.Index(readme, "<!-- agents:end -->")
	if start < 0 || end < start {
		t.Fatal("README.md has no agents:start/agents:end markers")
	}
	start += strings.Index(readme[start:], "\n") + 1
	want := agentTable()
	if readme[start:end] == want {
		return
	}
	if os.Getenv("UPDATE_README") != "" {
		if err := os.WriteFile(path, []byte(readme[:start]+want+readme[end:]), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatalf("README.md's agent table is stale; run UPDATE_README=1 go test ./internal/app\nwant:\n%s", want)
}
