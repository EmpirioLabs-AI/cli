package agents

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/EmpirioLabs-AI/cli/internal/catalog"
)

// Crush has no inline config, and its data directory's crush.json outranks
// both the project's and the user's. So each project gets a data directory
// of its own inside the CLI's directory, holding a crush.json written from
// the live catalog on every launch (the key stays in the environment, named
// by reference) and that project's Crush sessions. The provider id is
// empiriolabs-ai rather than the user's own provider names, so a model entry
// of theirs can never merge into ours.
func init() {
	register(Agent{
		Name:     "crush",
		Title:    "Crush",
		Binaries: []string{"crush"},
		Surface:  catalog.Chat,
		Install: map[string]string{
			"":        "npm install -g @charmland/crush",
			"darwin":  "brew install charmbracelet/tap/crush",
			"windows": "winget install charmbracelet.crush",
		},
		Docs: "https://docs.empiriolabs.ai/integrations#crush",
		Plan: planCrush,
	})
}

const crushProvider = "empiriolabs-ai"

func crushModel(m catalog.Model) map[string]any {
	entry := map[string]any{
		"id":                   m.ID,
		"name":                 m.Name(),
		"context_window":       contextFor(m),
		"default_max_tokens":   outputFor(m, 64_000),
		"can_reason":           m.Reasoning(),
		"supports_attachments": m.Vision(),
	}
	if p := m.Price(); p.Known {
		entry["cost_per_1m_in"] = p.Input
		entry["cost_per_1m_out"] = p.Output
		if p.CacheRead > 0 {
			entry["cost_per_1m_in_cached"] = p.CacheRead
		}
	}
	if effort, ok := m.Parameter("reasoning_effort"); ok && len(effort.Options) > 0 {
		var levels []string
		for _, o := range effort.Options {
			if o != "none" {
				levels = append(levels, o)
			}
		}
		if len(levels) > 0 {
			entry["reasoning_levels"] = levels
			var def string
			if json.Unmarshal(effort.Default, &def) == nil && def != "" {
				entry["default_reasoning_effort"] = def
			}
		}
	}
	return entry
}

func crushSlot(m catalog.Model) map[string]any {
	return map[string]any{"model": m.ID, "provider": crushProvider, "max_tokens": outputFor(m, 64_000)}
}

// crushDataDir is this project's Crush data directory inside the CLI's own.
func crushDataDir(runDir string) string {
	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(wd))))
	name := filepath.Base(wd)
	clean := strings.Map(func(r rune) rune {
		if r == '-' || r == '_' || r == '.' || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9') {
			return r
		}
		return '-'
	}, name)
	return filepath.Join(filepath.Dir(runDir), "crush", clean+"-"+hex.EncodeToString(sum[:4]))
}

func planCrush(c Context, binary string, args []string) (Plan, error) {
	main, fast := c.Main, c.Fast
	var models []map[string]any
	for _, m := range c.Catalog.For(catalog.Chat) {
		models = append(models, crushModel(m))
	}
	config := map[string]any{
		"$schema": "https://charm.land/crush.json",
		"providers": map[string]any{
			crushProvider: map[string]any{
				"name":            "EmpirioLabs AI",
				"type":            "openai-compat",
				"base_url":        c.BaseURL + "/v1",
				"api_key":         "$EMPIRIOLABS_API_KEY",
				"discover_models": false,
				"models":          models,
			},
		},
		"models": map[string]any{"large": crushSlot(main), "small": crushSlot(fast)},
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return Plan{}, err
	}
	dir := crushDataDir(c.RunDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Plan{}, err
	}
	head := []string{}
	if !hasFlag(args, "-D", "--data-dir") {
		head = append(head, "--data-dir", dir)
	}
	return Plan{
		Binary: binary,
		Args:   append(head, args...),
		Env: map[string]string{
			"EMPIRIOLABS_API_KEY":                c.Key,
			"CRUSH_DISABLE_METRICS":              "1",
			"CRUSH_DISABLE_PROVIDER_AUTO_UPDATE": "1",
		},
		// Rewritten from the live catalog on every launch; it holds no key.
		Persist: []File{{Path: filepath.Join(dir, "crush.json"), Data: append(data, '\n')}},
		Secrets: []string{c.Key},
		Summary: "Crush on " + main.Name() + " (fast: " + fast.Name() + ")",
	}, nil
}
