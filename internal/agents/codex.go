package agents

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/EmpirioLabs-AI/cli/internal/catalog"
	"github.com/EmpirioLabs-AI/cli/internal/guard"
)

// Codex speaks the OpenAI Responses API (POST /v1/responses).
//
// The provider goes in through `-c` overrides, which outrank
// ~/.codex/config.toml without editing it. The key travels in the process
// environment under the provider's env_key, never on the command line.
func init() {
	register(Agent{
		Name:     "codex",
		Aliases:  []string{"codex-cli"},
		Title:    "Codex CLI",
		Binaries: []string{"codex"},
		Surface:  catalog.Responses,
		Install: map[string]string{
			"":       "npm install -g @openai/codex",
			"darwin": "brew install --cask codex",
		},
		Docs:  "https://docs.empiriolabs.ai/integrations#codex-cli",
		Tools: true,
		Plan:  planCodex,
	})
}

func planCodex(c Context, binary string, args []string) (Plan, error) {
	if hasFlag(args, "--profile", "-p") {
		return Plan{}, errors.New("a Codex profile would replace the EmpirioLabs AI provider; run `codex` directly to use one")
	}
	if hasFlag(args, "--oss", "--local-provider") {
		return Plan{}, errors.New("--oss and --local-provider choose another provider; run `codex` directly to use them")
	}
	main := c.Main
	overrides := []string{
		`model_provider="empiriolabs"`,
		`model_providers.empiriolabs.name="EmpirioLabs AI"`,
		fmt.Sprintf(`model_providers.empiriolabs.base_url=%q`, c.BaseURL+"/v1"),
		`model_providers.empiriolabs.wire_api="responses"`,
		`model_providers.empiriolabs.env_key="EMPIRIOLABS_API_KEY"`,
		fmt.Sprintf(`model=%q`, main.ID),
		"model_context_window=" + strconv.Itoa(contextFor(main)),
		"model_auto_compact_token_limit=" + strconv.Itoa(compactAt(main)*9/10),
	}
	if main.Reasoning() {
		overrides = append(overrides, `model_reasoning_summary="auto"`)
	}
	if c.MCPURL != "" {
		// A streamable HTTP MCP server, authenticated from the same variable as
		// the provider, so the key stays out of the arguments.
		overrides = append(overrides,
			fmt.Sprintf(`mcp_servers.%s.url=%q`, MCPName, c.MCPURL),
			fmt.Sprintf(`mcp_servers.%s.bearer_token_env_var="EMPIRIOLABS_API_KEY"`, MCPName),
		)
	}
	var out []string
	for _, o := range overrides {
		out = append(out, "-c", o)
	}
	return Plan{
		Binary:  binary,
		Args:    append(out, args...),
		Env:     map[string]string{"EMPIRIOLABS_API_KEY": c.Key},
		Secrets: []string{c.Key},
		Summary: "Codex on " + main.Name(),
		// /model saves model and its effort to config.toml (the root table or a profile).
		Guards: []guard.Guard{&guard.TOMLKeys{Path: homeFile("CODEX_HOME", ".codex", "config.toml"), Keys: []string{"model", "model_reasoning_effort"}}},
	}, nil
}
