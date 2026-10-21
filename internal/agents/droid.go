package agents

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/EmpirioLabs-AI/cli/internal/catalog"
	"github.com/EmpirioLabs-AI/cli/internal/guard"
)

// Factory Droid merges a `--settings <file>` into its configuration for one
// process only. The file lists the catalog's models as custom models that
// call Chat Completions with the key read from the environment, so no Factory
// sign-in is needed and the user's ~/.factory/settings.json is not edited.
func init() {
	register(Agent{
		Name:     "droid",
		Aliases:  []string{"factory"},
		Title:    "Factory Droid",
		Binaries: []string{"droid"},
		Surface:  catalog.Chat,
		Install: map[string]string{
			"":        "curl -fsSL https://app.factory.ai/cli | sh",
			"windows": "irm https://app.factory.ai/cli/windows | iex",
		},
		Docs: "https://docs.empiriolabs.ai/integrations#factory-droid",
		Plan: planDroid,
	})
}

func droidID(m catalog.Model) string { return "custom:empiriolabs-" + m.ID }

func droidModel(c Context, m catalog.Model) map[string]any {
	return map[string]any{
		"id":              droidID(m),
		"model":           m.ID,
		"displayName":     m.Name() + " (EmpirioLabs AI)",
		"provider":        "generic-chat-completion-api",
		"baseUrl":         c.BaseURL + "/v1",
		"apiKey":          "${EMPIRIOLABS_API_KEY}",
		"maxOutputTokens": outputFor(m, 64_000),
		"maxContextLimit": contextFor(m),
		"supportsImages":  m.Vision(),
	}
}

func planDroid(c Context, binary string, args []string) (Plan, error) {
	main := c.Main
	var custom []map[string]any
	seen := map[string]bool{}
	add := func(m catalog.Model) {
		if m.ID != "" && !seen[m.ID] {
			seen[m.ID] = true
			custom = append(custom, droidModel(c, m))
		}
	}
	add(main)
	add(c.Fast)
	for _, m := range c.Catalog.For(catalog.Chat) {
		if m.IsFeatured && !strings.Contains(m.ID, ":") {
			add(m)
		}
	}
	settings := map[string]any{"model": droidID(main), "customModels": custom}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return Plan{}, err
	}
	file := filepath.Join(c.RunDir, "droid-settings-"+sessionID()+".json")
	head := []string{}
	if len(args) > 0 && args[0] == "exec" {
		head, args = []string{"exec"}, args[1:]
	}
	head = append(head, "--settings", file)
	if !hasFlag(args, "-m", "--model") && len(head) > 0 && head[0] == "exec" {
		head = append(head, "--model", droidID(main))
	}
	return Plan{
		Binary:  binary,
		Args:    append(head, args...),
		Env:     map[string]string{"EMPIRIOLABS_API_KEY": c.Key, "OTEL_SDK_DISABLED": "true"},
		Files:   []File{{Path: file, Data: data}},
		Secrets: []string{c.Key},
		Summary: "Droid on " + main.Name(),
		Guards:  []guard.Guard{&guard.JSONKeys{Path: homeFile("FACTORY_HOME", ".factory", "settings.json"), Keys: []string{"model"}}},
	}, nil
}
