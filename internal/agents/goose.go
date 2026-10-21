package agents

import (
	"strconv"

	"github.com/EmpirioLabs-AI/cli/internal/catalog"
)

// goose has EmpirioLabs AI built in as the provider `empiriolabs`, keyed by
// EMPIRIOLABS_API_KEY. Provider and model in the environment outrank the
// user's config.yaml. With no arguments the session starts with
// `goose session`: bare `goose` with no config file opens `goose configure`.
func init() {
	register(Agent{
		Name:     "goose",
		Title:    "goose",
		Binaries: []string{"goose"},
		Surface:  catalog.Chat,
		Install: map[string]string{
			"":        "curl -fsSL https://github.com/aaif-goose/goose/releases/download/stable/download_cli.sh | bash",
			"windows": `Invoke-WebRequest -Uri "https://raw.githubusercontent.com/aaif-goose/goose/main/download_cli.ps1" -OutFile download_cli.ps1; .\download_cli.ps1`,
		},
		Docs: "https://docs.empiriolabs.ai/integrations#goose",
		Plan: planGoose,
	})
}

func planGoose(c Context, binary string, args []string) (Plan, error) {
	main := c.Main
	if len(args) == 0 {
		args = []string{"session"}
	}
	return Plan{
		Binary: binary,
		Args:   args,
		Env: map[string]string{
			"GOOSE_PROVIDER":      "empiriolabs",
			"GOOSE_MODEL":         main.ID,
			"EMPIRIOLABS_API_KEY": c.Key,
			"GOOSE_CONTEXT_LIMIT": strconv.Itoa(contextFor(main)),
			// Also keeps the first-run consent question from writing to config.yaml.
			"GOOSE_TELEMETRY_OFF": "1",
		},
		Secrets: []string{c.Key},
		Summary: "goose on " + main.Name(),
	}, nil
}
