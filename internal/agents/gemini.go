package agents

import (
	"path/filepath"

	"github.com/EmpirioLabs-AI/cli/internal/catalog"
	"github.com/EmpirioLabs-AI/cli/internal/guard"
)

// Gemini CLI speaks Google's generateContent API, which EmpirioLabs AI serves
// for every chat model at /v1beta/models/{id}:generateContent.
//
// GEMINI_CLI_AUTH_OVERRIDE selects the API-key login for this process only;
// without it a user signed in with Google stays on their Google account, and
// setting a base URL alone picks a login this version refuses. Gemini CLI
// restarts itself after some settings change (exit code 199) and its own
// restart drops the override, so empirio restarts it instead. Its web search
// and web fetch tools call fixed Google models, so a session policy denies them.
func init() {
	register(Agent{
		Name:     "gemini",
		Aliases:  []string{"gemini-cli"},
		Title:    "Gemini CLI",
		Binaries: []string{"gemini"},
		Surface:  catalog.Gemini,
		Install:  map[string]string{"": "npm install -g @google/gemini-cli"},
		Docs:     "https://docs.empiriolabs.ai/integrations#gemini-cli",
		Plan:     planGemini,
	})
}

const geminiPolicy = `# Written by empirio for one Gemini CLI session and removed when it ends.
# These two tools call fixed Google models that are not part of this session.
[[rule]]
toolName = ["google_web_search", "web_fetch"]
decision = "deny"
priority = 900
denyMessage = "This tool is not available in this session."
`

func planGemini(c Context, binary string, args []string) (Plan, error) {
	main := c.Main
	policy := filepath.Join(c.RunDir, "gemini-policy-"+sessionID()+".toml")
	head := []string{"--policy", policy}
	if !hasFlag(args, "-m", "--model") {
		head = append(head, "--model", main.ID)
	}
	return Plan{
		Binary: binary,
		Args:   append(head, args...),
		Env: map[string]string{
			"GEMINI_API_KEY":            c.Key,
			"GOOGLE_GEMINI_BASE_URL":    c.BaseURL,
			"GEMINI_CLI_AUTH_OVERRIDE":  "gemini-api-key",
			"GEMINI_CLI_NO_RELAUNCH":    "true",
			"GEMINI_TELEMETRY_ENABLED":  "false",
			"GOOGLE_GENAI_USE_VERTEXAI": "false",
		},
		Unset:    []string{"GOOGLE_API_KEY", "GOOGLE_CLOUD_PROJECT", "GOOGLE_APPLICATION_CREDENTIALS"},
		Files:    []File{{Path: policy, Data: []byte(geminiPolicy)}},
		Secrets:  []string{c.Key},
		Summary:  "Gemini CLI on " + main.Name(),
		Relaunch: []int{199},
		// Saving any user setting during the session writes the override's login there.
		Guards: []guard.Guard{&guard.JSONKeys{
			Path: geminiSettings(),
			Keys: []string{"security.auth.selectedType", "model.name"},
		}},
	}, nil
}

func geminiSettings() string {
	return homeFile("GEMINI_CLI_HOME", "", filepath.Join(".gemini", "settings.json"))
}
