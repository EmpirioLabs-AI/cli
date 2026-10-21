package agents

import (
	"encoding/json"
	"strings"

	"github.com/EmpirioLabs-AI/cli/internal/catalog"
)

// OpenCode and Kilo CLI (an OpenCode fork) have EmpirioLabs AI built in as
// the provider `empiriolabs`. An inline config (OPENCODE_CONFIG_CONTENT,
// KILO_CONFIG_CONTENT) is merged after every config file, so it wins over the
// user's own and over a key saved with /connect, and the user's other
// settings (MCP servers, agents) stay in effect. The models the session is
// likely to use are described inline, so a model launched after the
// community model list was last updated still works with the right limits.
func init() {
	register(Agent{
		Name:     "opencode",
		Title:    "OpenCode",
		Binaries: []string{"opencode"},
		Surface:  catalog.Chat,
		Install: map[string]string{
			"":        "curl -fsSL https://opencode.ai/install | bash",
			"windows": "npm install -g opencode-ai",
		},
		Docs: "https://docs.empiriolabs.ai/integrations#opencode",
		Plan: func(c Context, binary string, args []string) (Plan, error) {
			return planOpenCode(c, binary, args, "OPENCODE_CONFIG_CONTENT", "https://opencode.ai/config.json", "OpenCode", nil)
		},
	})
	register(Agent{
		Name:     "kilo",
		Aliases:  []string{"kilocode", "kilo-code"},
		Title:    "Kilo CLI",
		Binaries: []string{"kilo"},
		Surface:  catalog.Chat,
		Install:  map[string]string{"": "npm install -g @kilocode/cli"},
		Docs:     "https://docs.empiriolabs.ai/integrations#kilo-cli",
		Plan: func(c Context, binary string, args []string) (Plan, error) {
			return planOpenCode(c, binary, args, "KILO_CONFIG_CONTENT", "https://app.kilo.ai/config.json", "Kilo CLI", map[string]string{
				"KILO_TELEMETRY_LEVEL": "off",
			})
		},
	})
}

func openCodeModel(m catalog.Model) map[string]any {
	entry := map[string]any{
		"name":      m.Name(),
		"tool_call": true,
		"reasoning": m.Reasoning(),
		"limit":     map[string]int{"context": contextFor(m), "output": outputFor(m, 64_000)},
	}
	if m.Vision() {
		entry["attachment"] = true
		entry["modalities"] = map[string][]string{"input": {"text", "image"}, "output": {"text"}}
	}
	if p := m.Price(); p.Known {
		cost := map[string]float64{"input": p.Input, "output": p.Output}
		if p.CacheRead > 0 {
			cost["cache_read"] = p.CacheRead
		}
		entry["cost"] = cost
	}
	return entry
}

func planOpenCode(c Context, binary string, args []string, contentEnv, schema, title string, extra map[string]string) (Plan, error) {
	main, fast := c.Main, c.Fast
	models := map[string]any{}
	add := func(m catalog.Model) {
		if m.ID != "" {
			models[m.ID] = openCodeModel(m)
		}
	}
	add(main)
	add(fast)
	for _, m := range c.Catalog.For(catalog.Chat) {
		if m.IsFeatured && !strings.Contains(m.ID, ":") {
			add(m)
		}
	}
	config := map[string]any{
		"$schema":     schema,
		"model":       "empiriolabs/" + main.ID,
		"small_model": "empiriolabs/" + fast.ID,
		"provider": map[string]any{
			"empiriolabs": map[string]any{
				"name": "EmpirioLabs AI",
				"options": map[string]any{
					"baseURL": c.BaseURL + "/v1",
					// A key saved with /connect would otherwise win over the environment.
					"apiKey": "{env:EMPIRIOLABS_API_KEY}",
				},
				"models": models,
			},
		},
	}
	content, err := json.Marshal(config)
	if err != nil {
		return Plan{}, err
	}
	env := map[string]string{
		contentEnv:            string(content),
		"EMPIRIOLABS_API_KEY": c.Key,
	}
	for k, v := range extra {
		env[k] = v
	}
	return Plan{
		Binary:  binary,
		Args:    args,
		Env:     env,
		Secrets: []string{c.Key},
		Summary: title + " on " + main.Name() + " (titles and summaries: " + fast.Name() + ")",
	}, nil
}
