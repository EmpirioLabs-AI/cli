package agents

import (
	"github.com/EmpirioLabs-AI/cli/internal/catalog"
)

// Qwen Code speaks OpenAI Chat Completions.
//
// The flags select the OpenAI-compatible login, the model and the base URL;
// they outrank the user's settings.json (including a discontinued Qwen OAuth
// login). The key stays in OPENAI_API_KEY in the process environment; the
// --openai-api-key flag would put it on the command line.
func init() {
	register(Agent{
		Name:     "qwen",
		Aliases:  []string{"qwen-code"},
		Title:    "Qwen Code",
		Binaries: []string{"qwen"},
		Surface:  catalog.Chat,
		Install:  map[string]string{"": "npm install -g @qwen-code/qwen-code"},
		Docs:     "https://docs.empiriolabs.ai/integrations#qwen-code",
		Tools:    true,
		Plan:     planQwen,
	})
}

func planQwen(c Context, binary string, args []string) (Plan, error) {
	main := c.Main
	head := []string{"--auth-type", "openai", "--openai-base-url", c.BaseURL + "/v1"}
	if !hasFlag(args, "-m", "--model") {
		head = append(head, "--model", main.ID)
	}
	env := map[string]string{
		"OPENAI_API_KEY":                c.Key,
		"OPENAI_BASE_URL":               c.BaseURL + "/v1",
		"OPENAI_MODEL":                  main.ID,
		"QWEN_USAGE_STATISTICS_ENABLED": "false",
	}
	var files []File
	// The EmpirioLabs AI tools come in through a session system-defaults file (defaults.go).
	if extra, defaults, ok := mcpDefaults(c, "QWEN_CODE_SYSTEM_DEFAULTS_PATH",
		`C:\ProgramData\qwen-code`, "/Library/Application Support/QwenCode", "/etc/qwen-code", "qwen"); ok {
		for k, v := range extra {
			env[k] = v
		}
		files = defaults
	}
	return Plan{
		Binary:  binary,
		Args:    append(head, args...),
		Env:     env,
		Files:   files,
		Secrets: []string{c.Key},
		Summary: "Qwen Code on " + main.Name(),
	}, nil
}
