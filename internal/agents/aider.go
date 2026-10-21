package agents

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/EmpirioLabs-AI/cli/internal/catalog"
)

// Aider speaks OpenAI Chat Completions through litellm (model `openai/<id>`).
//
// The base URL goes in as a flag because a project .env or .aider.conf.yml
// outranks the environment, and AIDER_OPENAI_API_KEY outranks a key in those
// files. Two session files give aider the model's limits and prices, and an
// empty `aider/extra_params` entry, loaded last, stops a repository's model
// settings from sending the request (and its key) to another host.
func init() {
	register(Agent{
		Name:     "aider",
		Title:    "Aider",
		Binaries: []string{"aider"},
		Surface:  catalog.Chat,
		Install: map[string]string{
			"":        "curl -LsSf https://aider.chat/install.sh | sh",
			"windows": `powershell -ExecutionPolicy ByPass -c "irm https://aider.chat/install.ps1 | iex"`,
		},
		Docs: "https://docs.empiriolabs.ai/integrations#aider",
		Plan: planAider,
	})
}

func aiderMetadata(m catalog.Model) map[string]any {
	entry := map[string]any{
		"max_input_tokens":          contextFor(m),
		"max_output_tokens":         outputFor(m, 64_000),
		"max_tokens":                outputFor(m, 64_000),
		"litellm_provider":          "openai",
		"mode":                      "chat",
		"supports_function_calling": true,
		"supports_vision":           m.Vision(),
	}
	if p := m.Price(); p.Known {
		entry["input_cost_per_token"] = p.Input / 1_000_000
		entry["output_cost_per_token"] = p.Output / 1_000_000
	}
	return entry
}

func planAider(c Context, binary string, args []string) (Plan, error) {
	main, fast := c.Main, c.Fast
	model := "openai/" + main.ID
	weak := "openai/" + fast.ID
	metadata := map[string]any{model: aiderMetadata(main), weak: aiderMetadata(fast)}
	metaJSON, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return Plan{}, err
	}
	var settings strings.Builder
	settings.WriteString("# Written by empirio for one aider session and removed when it ends.\n")
	settings.WriteString("- name: aider/extra_params\n  extra_params: {}\n")
	fmt.Fprintf(&settings, "- name: %s\n  edit_format: diff\n  weak_model_name: %s\n  use_repo_map: true\n", model, weak)
	if fast.ID != main.ID {
		fmt.Fprintf(&settings, "- name: %s\n  edit_format: diff\n  use_repo_map: true\n", weak)
	}
	id := sessionID()
	metaPath := filepath.Join(c.RunDir, "aider-metadata-"+id+".json")
	settingsPath := filepath.Join(c.RunDir, "aider-settings-"+id+".yml")

	head := []string{
		"--openai-api-base", c.BaseURL + "/v1",
		"--model-metadata-file", metaPath,
		"--model-settings-file", settingsPath,
	}
	if !hasFlag(args, "--model") {
		head = append(head, "--model", model)
	}
	return Plan{
		Binary: binary,
		Args:   append(head, args...),
		Env: map[string]string{
			"AIDER_OPENAI_API_KEY": c.Key,
			// The limits and prices come from the session file; no price list download.
			"LITELLM_LOCAL_MODEL_COST_MAP": "True",
		},
		Files: []File{
			{Path: metaPath, Data: append(metaJSON, '\n')},
			{Path: settingsPath, Data: []byte(settings.String())},
		},
		Secrets: []string{c.Key},
		Summary: "Aider on " + main.Name() + " (weak model: " + fast.Name() + ")",
	}, nil
}
