package agents

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/EmpirioLabs-AI/cli/internal/catalog"
	"github.com/EmpirioLabs-AI/cli/internal/guard"
)

// Claude Code speaks the Anthropic Messages API (POST /v1/messages).
//
// The session's settings go in through `--settings <file>`: flag settings
// outrank ~/.claude/settings.json, whose `env` block would otherwise win over
// anything in the process environment, so a user who once pointed Claude Code
// somewhere else still gets EmpirioLabs AI here. The file holds the key, so it
// is written mode 0600 in the CLI's own directory and removed when the session
// ends; the key never appears on the command line.
func init() {
	register(Agent{
		Name:     "claude",
		Aliases:  []string{"claude-code"},
		Title:    "Claude Code",
		Binaries: []string{"claude"},
		Surface:  catalog.Messages,
		Install: map[string]string{
			"":        "curl -fsSL https://claude.ai/install.sh | bash",
			"windows": "irm https://claude.ai/install.ps1 | iex",
		},
		Docs: "https://docs.empiriolabs.ai/integrations#claude-code",
		Plan: planClaude,
	})
}

func planClaude(c Context, binary string, args []string) (Plan, error) {
	if hasFlag(args, "--settings") {
		return Plan{}, errors.New("empirio passes its own --settings to Claude Code; run `claude` directly to use yours")
	}
	main, fast := c.Main, c.Fast
	env := map[string]string{
		"ANTHROPIC_BASE_URL":   c.BaseURL,
		"ANTHROPIC_AUTH_TOKEN": c.Key,
		"ANTHROPIC_API_KEY":    "",

		"ANTHROPIC_MODEL":                            main.ID,
		"ANTHROPIC_DEFAULT_OPUS_MODEL":               main.ID,
		"ANTHROPIC_DEFAULT_OPUS_MODEL_NAME":          main.Name(),
		"ANTHROPIC_DEFAULT_OPUS_MODEL_DESCRIPTION":   describe(main),
		"ANTHROPIC_DEFAULT_SONNET_MODEL":             main.ID,
		"ANTHROPIC_DEFAULT_SONNET_MODEL_NAME":        main.Name(),
		"ANTHROPIC_DEFAULT_SONNET_MODEL_DESCRIPTION": describe(main),
		"ANTHROPIC_DEFAULT_FABLE_MODEL":              main.ID,
		"ANTHROPIC_DEFAULT_FABLE_MODEL_NAME":         main.Name(),
		"ANTHROPIC_DEFAULT_FABLE_MODEL_DESCRIPTION":  describe(main),
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":              fast.ID,
		"ANTHROPIC_DEFAULT_HAIKU_MODEL_NAME":         fast.Name(),
		"ANTHROPIC_DEFAULT_HAIKU_MODEL_DESCRIPTION":  describe(fast),
		"ANTHROPIC_SMALL_FAST_MODEL":                 fast.ID, // read by older versions

		"CLAUDE_CODE_MAX_CONTEXT_TOKENS":  strconv.Itoa(contextFor(main)),
		"CLAUDE_CODE_AUTO_COMPACT_WINDOW": strconv.Itoa(compactAt(main)),
		"CLAUDE_CODE_MAX_OUTPUT_TOKENS":   strconv.Itoa(outputFor(main, 64_000)),
		// The public edge allows a request 15 minutes.
		"API_TIMEOUT_MS": "900000",
		// A per-request header block in the system prompt changes every turn,
		// so the model service could never reuse the cached prompt.
		"CLAUDE_CODE_ATTRIBUTION_HEADER": "0",
		"DISABLE_TELEMETRY":              "1",
		"DISABLE_ERROR_REPORTING":        "1",
	}
	settings := map[string]any{
		"env":   env,
		"model": main.ID,
		// WebSearch is a server tool of Anthropic's own API; elsewhere it returns
		// nothing and the model has to recover. WebFetch keeps working.
		"permissions": map[string]any{"deny": []string{"WebSearch"}},
		"modelPicker": map[string]any{
			"options":               claudePicker(c),
			"replaceBuiltInOptions": true,
		},
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return Plan{}, err
	}
	file := filepath.Join(c.RunDir, "claude-settings-"+sessionID()+".json")
	return Plan{
		Binary:  binary,
		Args:    append([]string{"--settings", file}, args...),
		Files:   []File{{Path: file, Data: data}},
		Secrets: []string{c.Key},
		Summary: "Claude Code on " + main.Name() + " (fast: " + fast.Name() + ")",
		// /model, then "set as default", saves `model` to the user's own settings.
		Guards: []guard.Guard{&guard.JSONKeys{Path: homeFile("CLAUDE_CONFIG_DIR", ".claude", "settings.json"), Keys: []string{"model"}}},
	}, nil
}

// claudePicker lists the session's models first, then the catalog's featured
// models, in catalog order. Regional variants are left to --model.
func claudePicker(c Context) []map[string]string {
	var rows []map[string]string
	seen := map[string]bool{}
	add := func(m catalog.Model) {
		if m.ID == "" || seen[m.ID] {
			return
		}
		seen[m.ID] = true
		rows = append(rows, map[string]string{"model": m.ID, "label": m.Name(), "description": describe(m)})
	}
	add(c.Main)
	add(c.Fast)
	for _, m := range c.Catalog.For(catalog.Messages) {
		if m.IsFeatured && !strings.Contains(m.ID, ":") {
			add(m)
		}
	}
	return rows
}
