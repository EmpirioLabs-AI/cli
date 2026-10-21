// Package catalog reads the live EmpirioLabs AI model catalog and picks the
// models each coding agent can use.
//
// Nothing here names a model. Which models an agent can run, the default one
// and the fast one for background work all come from the catalog at launch
// time, so a model launched tomorrow shows up without a new release.
package catalog

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"
)

// Surface is the API shape an agent speaks.
type Surface string

const (
	Messages  Surface = "messages"  // Anthropic Messages: POST /v1/messages
	Responses Surface = "responses" // OpenAI Responses: POST /v1/responses
	Chat      Surface = "chat"      // OpenAI Chat Completions: POST /v1/chat/completions
	Gemini    Surface = "gemini"    // Google generateContent: POST /v1beta/models/{id}:generateContent
)

// Catalog is the response of GET /v1/models?available=true, plus when it was read.
type Catalog struct {
	FetchedAt time.Time `json:"fetched_at"`
	Models    []Model   `json:"data"`
}

// Model is one catalog entry, reduced to the fields the CLI reads.
type Model struct {
	ID                 string          `json:"id"`
	DisplayName        string          `json:"display_name"`
	Author             string          `json:"author"`
	Category           string          `json:"category"`
	Type               string          `json:"type"`
	Status             string          `json:"status"`
	Features           []string        `json:"features"`
	Aliases            []string        `json:"aliases"`
	InputModalities    []string        `json:"input_modalities"`
	ContextWindow      int             `json:"context_window"`
	MaxOutputTokens    int             `json:"max_output_tokens"`
	SupportedEndpoints []string        `json:"supported_endpoints"`
	IsFeatured         bool            `json:"is_featured"`
	IsNew              bool            `json:"is_new"`
	ReleasedAt         string          `json:"model_released_at"`
	Pricing            json.RawMessage `json:"pricing"`
	Capabilities       struct {
		Images    bool `json:"images"`
		Reasoning bool `json:"reasoning"`
		WebSearch bool `json:"web_search"`
	} `json:"capabilities"`
	Parameters []Parameter `json:"supported_parameters"`
}

// Parameter is one control a model accepts.
type Parameter struct {
	Name    string          `json:"name"`
	Type    string          `json:"type"`
	Options []string        `json:"options"`
	Default json.RawMessage `json:"default"`
}

// Name is the display name, or the id when the catalog has none.
func (m Model) Name() string {
	if strings.TrimSpace(m.DisplayName) != "" {
		return m.DisplayName
	}
	return m.ID
}

// Endpoint reports whether the model lists the API shape of surface.
func (m Model) Endpoint(surface Surface) bool {
	for _, raw := range m.SupportedEndpoints {
		e := strings.TrimSpace(raw)
		switch surface {
		case Messages:
			if strings.HasSuffix(e, " /v1/messages") || e == "/v1/messages" {
				return true
			}
		case Responses:
			if strings.HasSuffix(e, " /v1/responses") || e == "/v1/responses" {
				return true
			}
		case Chat:
			if strings.HasSuffix(e, " /v1/chat/completions") || e == "/v1/chat/completions" {
				return true
			}
		case Gemini:
			if strings.Contains(e, "/v1beta/models/") && strings.HasSuffix(e, ":generateContent") {
				return true
			}
		}
	}
	return false
}

// HasFeature reports whether the catalog lists feature for the model.
func (m Model) HasFeature(feature string) bool {
	for _, f := range m.Features {
		if strings.EqualFold(f, feature) {
			return true
		}
	}
	return false
}

// HasParameter reports whether the model accepts the named control.
func (m Model) HasParameter(name string) bool {
	for _, p := range m.Parameters {
		if p.Name == name {
			return true
		}
	}
	return false
}

// Parameter returns the named control.
func (m Model) Parameter(name string) (Parameter, bool) {
	for _, p := range m.Parameters {
		if p.Name == name {
			return p, true
		}
	}
	return Parameter{}, false
}

// Reasoning reports whether the model thinks before it answers.
func (m Model) Reasoning() bool {
	return m.Capabilities.Reasoning || m.HasFeature("reasoning")
}

// Vision reports whether the model reads images.
func (m Model) Vision() bool {
	if m.Capabilities.Images {
		return true
	}
	for _, in := range m.InputModalities {
		if in == "image" {
			return true
		}
	}
	return false
}

// Agentic reports whether a coding agent can drive the model: a text model
// that is available and calls tools.
func (m Model) Agentic() bool {
	if m.Status != "" && m.Status != "available" {
		return false
	}
	if m.Category != "" && m.Category != "text" {
		return false
	}
	return m.HasFeature("function_calling")
}

// Usable reports whether an agent speaking surface can run the model.
func (m Model) Usable(surface Surface) bool {
	return m.Agentic() && m.Endpoint(surface)
}

// Price is the model's price per million tokens.
type Price struct {
	Input, Output, CacheRead float64
	Tiered                   bool // the price rises with prompt length; these are the first tier's
	Known                    bool
}

type rate struct {
	Prompt     string `json:"prompt"`
	Completion string `json:"completion"`
	CacheRead  string `json:"input_cache_read"`
}

func perMillion(s string) (float64, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return 0, false
	}
	return v * 1_000_000, true
}

// Price reads the catalog's per-token price. A tiered price reports its first tier.
func (m Model) Price() Price {
	if len(m.Pricing) == 0 {
		return Price{}
	}
	var one rate
	tiered := false
	if m.Pricing[0] == '[' {
		var tiers []rate
		if json.Unmarshal(m.Pricing, &tiers) != nil || len(tiers) == 0 {
			return Price{}
		}
		one, tiered = tiers[0], len(tiers) > 1
	} else if json.Unmarshal(m.Pricing, &one) != nil {
		return Price{}
	}
	in, okIn := perMillion(one.Prompt)
	out, okOut := perMillion(one.Completion)
	cache, _ := perMillion(one.CacheRead)
	return Price{Input: in, Output: out, CacheRead: cache, Tiered: tiered, Known: okIn && okOut}
}

// Find returns the model an id or alias names, ignoring case.
func (c *Catalog) Find(id string) (Model, bool) {
	if c == nil {
		return Model{}, false
	}
	want := strings.ToLower(strings.TrimSpace(id))
	if want == "" {
		return Model{}, false
	}
	for _, m := range c.Models {
		if strings.ToLower(m.ID) == want {
			return m, true
		}
	}
	for _, m := range c.Models {
		for _, alias := range m.Aliases {
			if strings.ToLower(alias) == want {
				return m, true
			}
		}
	}
	return Model{}, false
}

// For lists the models an agent speaking surface can run, in catalog order.
func (c *Catalog) For(surface Surface) []Model {
	if c == nil {
		return nil
	}
	var out []Model
	for _, m := range c.Models {
		if m.Usable(surface) {
			out = append(out, m)
		}
	}
	return out
}

// Default is the model a session starts on: the first featured model the
// agent can run, in the catalog's own order, else the first one it can run.
func (c *Catalog) Default(surface Surface) (Model, bool) {
	usable := c.For(surface)
	for _, m := range usable {
		if m.IsFeatured && !m.Variant() {
			return m, true
		}
	}
	for _, m := range usable {
		if !m.Variant() {
			return m, true
		}
	}
	if len(usable) > 0 {
		return usable[0], true
	}
	return Model{}, false
}

// fastContextFloor keeps a background model able to read what the main one wrote.
const fastContextFloor = 128_000

// Variant reports whether the id names a regional or capability variant
// (`model:variant1`), which a user picks on purpose, never by default.
func (m Model) Variant() bool { return strings.Contains(m.ID, ":") }

// Fast is the model for background work (titles, summaries, quick reads): the
// cheapest featured model by the main model's author, else the cheapest
// featured model, that the agent can run and that keeps a working context.
// It falls back to main.
func (c *Catalog) Fast(surface Surface, main Model) Model {
	cheapest := func(sameAuthor bool) *Model {
		var best *Model
		bestCost := math.MaxFloat64
		for _, m := range c.For(surface) {
			if !m.IsFeatured || m.Variant() || m.ID == main.ID {
				continue
			}
			if sameAuthor && m.Author != main.Author {
				continue
			}
			if m.ContextWindow > 0 && m.ContextWindow < fastContextFloor {
				continue
			}
			p := m.Price()
			if !p.Known {
				continue
			}
			// An agent reads far more than it writes.
			if cost := 3*p.Input + p.Output; cost < bestCost {
				mm := m
				best, bestCost = &mm, cost
			}
		}
		return best
	}
	best := cheapest(true)
	if best == nil {
		best = cheapest(false)
	}
	if best == nil {
		return main
	}
	if mp, bp := main.Price(), best.Price(); mp.Known && 3*mp.Input+mp.Output <= 3*bp.Input+bp.Output {
		return main
	}
	return *best
}
