package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/EmpirioLabs-AI/cli/internal/agents"
	"github.com/EmpirioLabs-AI/cli/internal/api"
	"github.com/EmpirioLabs-AI/cli/internal/catalog"
	"github.com/EmpirioLabs-AI/cli/internal/config"
)

type modelRow struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	ContextWindow int      `json:"context_window"`
	MaxOutput     int      `json:"max_output_tokens"`
	InputPer1M    *float64 `json:"input_per_1m,omitempty"`
	OutputPer1M   *float64 `json:"output_per_1m,omitempty"`
	Tiered        bool     `json:"tiered_pricing"`
	Reasoning     bool     `json:"reasoning"`
	Vision        bool     `json:"vision"`
	Featured      bool     `json:"featured"`
	Agents        []string `json:"agents"`
}

// agentsFor names the agents that can run a model.
func agentsFor(m catalog.Model) []string {
	var out []string
	for _, a := range agents.All() {
		if m.Usable(a.Surface) {
			out = append(out, a.Name)
		}
	}
	return out
}

func models(args []string) error {
	var agentName, search string
	asJSON, all := false, false
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--json":
			asJSON = true
		case a == "--all":
			all = true
		case a == "--agent" && i+1 < len(args):
			agentName = args[i+1]
			i++
		case strings.HasPrefix(a, "--agent="):
			agentName = strings.TrimPrefix(a, "--agent=")
		case !strings.HasPrefix(a, "-") && search == "":
			search = strings.ToLower(a)
		default:
			return fmt.Errorf("unknown argument %q (usage: empirio models [--agent <agent>] [--all] [--json] [search])", a)
		}
	}
	var agent *agents.Agent
	if agentName != "" {
		a, ok := agents.Get(agentName)
		if !ok {
			return fmt.Errorf("unknown agent %q", agentName)
		}
		agent = &a
	}
	cat, err := loadCatalog(api.New(Version), false)
	if err != nil {
		return err
	}
	cfg, _ := config.Load()
	defaultSurface := catalog.Chat
	if agent != nil {
		defaultSurface = agent.Surface
	}
	def, _ := cat.Default(defaultSurface)
	if cfg.Model != "" {
		if m, ok := cat.Find(cfg.Model); ok {
			def = m
		}
	}
	fast := cat.Fast(defaultSurface, def)
	if cfg.FastModel != "" {
		if m, ok := cat.Find(cfg.FastModel); ok {
			fast = m
		}
	}

	var rows []modelRow
	for _, m := range cat.Models {
		if !m.Agentic() {
			continue
		}
		if agent != nil && !m.Usable(agent.Surface) {
			continue
		}
		if !all && agent == nil && len(agentsFor(m)) == 0 {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(m.ID+" "+m.Name()+" "+m.Author), search) {
			continue
		}
		row := modelRow{ID: m.ID, Name: m.Name(), ContextWindow: m.ContextWindow, MaxOutput: m.MaxOutputTokens,
			Reasoning: m.Reasoning(), Vision: m.Vision(), Featured: m.IsFeatured, Agents: agentsFor(m)}
		if p := m.Price(); p.Known {
			in, out := p.Input, p.Output
			row.InputPer1M, row.OutputPer1M, row.Tiered = &in, &out, p.Tiered
		}
		rows = append(rows, row)
	}
	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"default": def.ID, "fast": fast.ID, "models": rows})
	}
	if len(rows) == 0 {
		return fmt.Errorf("no model matches")
	}
	title := "Models for coding agents"
	if agent != nil {
		title = "Models " + agent.Title + " can run"
	}
	fmt.Fprintf(stdout, "%s (live catalog, %d)\n\n", bold(title), len(rows))
	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  MODEL\tCONTEXT\tINPUT\tOUTPUT\tNAME")
	for _, r := range rows {
		mark := " "
		switch r.ID {
		case def.ID:
			mark = "*"
		case fast.ID:
			mark = "+"
		}
		in, out := "-", "-"
		if r.InputPer1M != nil {
			in, out = "$"+agents.Money(*r.InputPer1M), "$"+agents.Money(*r.OutputPer1M)
			if r.Tiered {
				in += "+"
				out += "+"
			}
		}
		ctx := "-"
		if r.ContextWindow > 0 {
			ctx = agents.Tokens(r.ContextWindow)
		}
		fmt.Fprintf(w, "%s %s\t%s\t%s\t%s\t%s\n", mark, r.ID, ctx, in, out, r.Name)
	}
	w.Flush()
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, dim("Prices per 1M tokens; + rises with prompt length. * default model, + fast model."))
	fmt.Fprintln(stdout, dim("Start on one: empirio claude --model <id>    Make it your default: empirio model <id>"))
	return nil
}

// modelCmd shows or sets the saved default models.
func modelCmd(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	fast := false
	var id string
	for _, a := range args {
		switch {
		case a == "--fast":
			fast = true
		case a == "--reset":
			cfg.Model, cfg.FastModel = "", ""
			if err := config.Save(cfg); err != nil {
				return err
			}
			okf("Sessions use the catalog's default models again.")
			return nil
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("unknown flag %q (usage: empirio model [<id>] [--fast] [--reset])", a)
		default:
			id = a
		}
	}
	client := api.New(Version)
	cat, err := loadCatalog(client, false)
	if err != nil {
		return err
	}
	if id == "" {
		show := func(label, saved string, fallback catalog.Model) {
			if saved != "" {
				fmt.Fprintf(stdout, "%-12s %s (your default)\n", label, saved)
			} else {
				fmt.Fprintf(stdout, "%-12s %s (catalog default)\n", label, fallback.ID)
			}
		}
		def, _ := cat.Default(catalog.Chat)
		show("model", cfg.Model, def)
		show("fast model", cfg.FastModel, cat.Fast(catalog.Chat, def))
		fmt.Fprintln(stdout, dim("Change it with `empirio model <id>` (add --fast for the fast model); `empirio models` lists them."))
		return nil
	}
	m, ok := cat.Find(id)
	if !ok {
		if cat, err = loadCatalog(client, true); err == nil {
			m, ok = cat.Find(id)
		}
	}
	if !ok {
		return fmt.Errorf("%q is not in the EmpirioLabs AI catalog; `empirio models` lists them", id)
	}
	if !m.Agentic() {
		return fmt.Errorf("%s cannot drive a coding agent: %s", m.ID, unusableReason(m, catalog.Chat))
	}
	if fast {
		cfg.FastModel = m.ID
	} else {
		cfg.Model = m.ID
	}
	if err := config.Save(cfg); err != nil {
		return err
	}
	what := "default model"
	if fast {
		what = "fast model"
	}
	okf("Your %s is now %s (%s).", what, m.ID, m.Name())
	if missing := missingAgents(m); len(missing) > 0 {
		notef("%s keep the catalog default, because they cannot run it.", strings.Join(missing, ", "))
	}
	return nil
}

func missingAgents(m catalog.Model) []string {
	var out []string
	for _, a := range agents.All() {
		if !m.Usable(a.Surface) {
			out = append(out, a.Title)
		}
	}
	return out
}
