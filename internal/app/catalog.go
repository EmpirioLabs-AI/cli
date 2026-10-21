package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/EmpirioLabs-AI/cli/internal/agents"
	"github.com/EmpirioLabs-AI/cli/internal/api"
	"github.com/EmpirioLabs-AI/cli/internal/catalog"
	"github.com/EmpirioLabs-AI/cli/internal/config"
)

// The catalog is cached so a launch does not wait on the network, and read
// again after catalogTTL so new models appear the same day they launch.
const catalogTTL = 6 * time.Hour

func cachePath() string { return filepath.Join(config.Dir(), "models.json") }

func readCache() *catalog.Catalog {
	b, err := os.ReadFile(cachePath())
	if err != nil {
		return nil
	}
	var c catalog.Catalog
	if json.Unmarshal(b, &c) != nil || len(c.Models) == 0 {
		return nil
	}
	return &c
}

func writeCache(c *catalog.Catalog) {
	if os.MkdirAll(config.Dir(), 0o700) != nil {
		return
	}
	if b, err := json.Marshal(c); err == nil {
		_ = config.WriteFileAtomic(cachePath(), b, 0o600)
	}
}

// loadCatalog returns the model catalog: the cache while it is fresh, else the
// live list, else (offline) the last list read.
func loadCatalog(client *api.Client, fresh bool) (*catalog.Catalog, error) {
	cached := readCache()
	if !fresh && cached != nil && timeSince(cached.FetchedAt) < catalogTTL {
		return cached, nil
	}
	timeout := 20 * time.Second
	if cached != nil {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	live, err := client.Models(ctx)
	if err != nil {
		if cached != nil {
			return cached, nil
		}
		return nil, fmt.Errorf("could not read the model list from %s: %w", client.BaseURL, err)
	}
	writeCache(live)
	return live, nil
}

// choice is the pair of models a session runs on.
type choice struct {
	Main, Fast catalog.Model
	Notes      []string
}

// pickModels resolves the session's models. An explicit --model that cannot
// run in the agent is an error; a saved default that cannot is skipped with a note.
func pickModels(client *api.Client, cat *catalog.Catalog, agent agents.Agent, flagModel, flagFast string) (choice, *catalog.Catalog, error) {
	cfg, _ := config.Load()
	var ch choice
	resolve := func(id, what string, explicit bool) (catalog.Model, bool, error) {
		m, ok := cat.Find(id)
		if !ok {
			// A model launched since the cache was written.
			if fresh, err := loadCatalog(client, true); err == nil {
				cat = fresh
				m, ok = cat.Find(id)
			}
		}
		switch {
		case !ok && explicit:
			return m, false, fmt.Errorf("%q is not in the EmpirioLabs AI catalog; `empirio models` lists the models %s can run", id, agent.Title)
		case !ok:
			ch.Notes = append(ch.Notes, fmt.Sprintf("your saved %s %q is no longer in the catalog, so this session uses the catalog default", what, id))
			return m, false, nil
		case !m.Usable(agent.Surface) && explicit:
			return m, false, fmt.Errorf("%s cannot run %s: %s", agent.Title, m.ID, unusableReason(m, agent.Surface))
		case !m.Usable(agent.Surface):
			ch.Notes = append(ch.Notes, fmt.Sprintf("%s cannot run your saved %s %s, so this session uses the catalog default", agent.Title, what, m.ID))
			return m, false, nil
		}
		return m, true, nil
	}

	mainID, explicit := flagModel, flagModel != ""
	if mainID == "" {
		mainID = strings.TrimSpace(os.Getenv("EMPIRIO_MODEL"))
		explicit = mainID != ""
	}
	if mainID == "" {
		mainID = cfg.Model
	}
	if mainID != "" {
		m, ok, err := resolve(mainID, "model", explicit)
		if err != nil {
			return ch, cat, err
		}
		if ok {
			ch.Main = m
		}
	}
	if ch.Main.ID == "" {
		m, ok := cat.Default(agent.Surface)
		if !ok {
			return ch, cat, fmt.Errorf("the catalog lists no model %s can run right now", agent.Title)
		}
		ch.Main = m
	}

	fastID, fastExplicit := flagFast, flagFast != ""
	if fastID == "" {
		fastID = cfg.FastModel
	}
	if fastID != "" {
		m, ok, err := resolve(fastID, "fast model", fastExplicit)
		if err != nil {
			return ch, cat, err
		}
		if ok {
			ch.Fast = m
		}
	}
	if ch.Fast.ID == "" {
		// An explicit main model with no fast model runs everything on that model.
		if explicit {
			ch.Fast = ch.Main
		} else {
			ch.Fast = cat.Fast(agent.Surface, ch.Main)
		}
	}
	return ch, cat, nil
}

func unusableReason(m catalog.Model, surface catalog.Surface) string {
	switch {
	case m.Category != "" && m.Category != "text":
		return "it is not a chat model"
	case !m.HasFeature("function_calling"):
		return "it does not call tools"
	case !m.Endpoint(surface):
		return "it does not support " + endpointName(surface)
	case m.Status != "" && m.Status != "available":
		return "it is not available right now"
	}
	return "it is not available for coding agents"
}

func endpointName(s catalog.Surface) string {
	switch s {
	case catalog.Messages:
		return "the Messages API (POST /v1/messages)"
	case catalog.Responses:
		return "the Responses API (POST /v1/responses)"
	case catalog.Gemini:
		return "the Gemini API (generateContent)"
	}
	return "Chat Completions (POST /v1/chat/completions)"
}
