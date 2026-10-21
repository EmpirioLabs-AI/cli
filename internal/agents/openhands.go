package agents

import (
	"github.com/EmpirioLabs-AI/cli/internal/catalog"
)

// OpenHands CLI reads LLM_MODEL, LLM_BASE_URL and LLM_API_KEY only with
// --override-with-envs, and then does not save them. All three are always set:
// a base URL left to the user's stored settings or a project .env would
// receive the key.
func init() {
	register(Agent{
		Name:     "openhands",
		Title:    "OpenHands CLI",
		Binaries: []string{"openhands"},
		Surface:  catalog.Chat,
		Install:  map[string]string{"": "uv tool install openhands --python 3.12"},
		Docs:     "https://docs.empiriolabs.ai/integrations#openhands",
		Plan:     planOpenHands,
	})
}

func planOpenHands(c Context, binary string, args []string) (Plan, error) {
	main := c.Main
	head := []string{}
	if !hasFlag(args, "--override-with-envs") {
		head = append(head, "--override-with-envs")
	}
	return Plan{
		Binary: binary,
		Args:   append(head, args...),
		Env: map[string]string{
			"LLM_MODEL":    "openai/" + main.ID,
			"LLM_BASE_URL": c.BaseURL + "/v1",
			"LLM_API_KEY":  c.Key,
		},
		Secrets: []string{c.Key},
		Summary: "OpenHands on " + main.Name(),
	}, nil
}
