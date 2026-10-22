package app

import (
	"encoding/json"
	"fmt"
	"sort"
	"text/tabwriter"

	"github.com/EmpirioLabs-AI/cli/internal/agents"
	"github.com/EmpirioLabs-AI/cli/internal/catalog"
)

// agentRecord is one agent as `empirio agents --json` prints it. Every
// release publishes this list as agents.json, and the documentation's agent
// table is generated from it, so the CLI is the one source of which agents
// are supported and how each is installed.
type agentRecord struct {
	Name    string            `json:"name"`
	Aliases []string          `json:"aliases,omitempty"`
	Title   string            `json:"title"`
	Command string            `json:"command"`
	API     string            `json:"api"`
	APIName string            `json:"api_name"`
	Install map[string]string `json:"install"`
	Docs    string            `json:"docs"`
	// Tools: the agent gets the EmpirioLabs AI MCP server (web search, media
	// generation, the catalog, jobs, the balance) for each session.
	Tools bool `json:"tools"`
}

func agentRecords() []agentRecord {
	var out []agentRecord
	for _, a := range agents.All() {
		install := map[string]string{}
		keys := make([]string, 0, len(a.Install))
		for k := range a.Install {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			name := k
			if name == "" {
				name = "default"
			}
			install[name] = a.Install[k]
		}
		out = append(out, agentRecord{
			Name: a.Name, Aliases: a.Aliases, Title: a.Title, Command: "empirio " + a.Name,
			API: string(a.Surface), APIName: apiName(a.Surface), Install: install, Docs: a.Docs, Tools: a.Tools,
		})
	}
	return out
}

func agentList(args []string) error {
	asJSON := false
	for _, a := range args {
		if a != "--json" {
			return fmt.Errorf("unknown flag %q (usage: empirio agents [--json])", a)
		}
		asJSON = true
	}
	records := agentRecords()
	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"version": Version, "agents": records})
	}
	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "COMMAND\tAGENT\tINSTALLED")
	for _, a := range agents.All() {
		state := "no"
		if path := a.Find(); path != "" {
			state = "yes"
		}
		fmt.Fprintf(w, "empirio %s\t%s\t%s\n", a.Name, a.Title, state)
	}
	w.Flush()
	fmt.Fprintln(stdout, dim("`empirio doctor` shows how to install each agent."))
	return nil
}

func apiName(s catalog.Surface) string {
	switch s {
	case catalog.Messages:
		return "Anthropic Messages"
	case catalog.Responses:
		return "OpenAI Responses"
	case catalog.Gemini:
		return "Google Gemini"
	}
	return "OpenAI Chat Completions"
}
