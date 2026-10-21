package catalog

import (
	"encoding/json"
	"testing"
)

const fixture = `{"data": [
 {"id": "image-1", "category": "image", "is_featured": true, "status": "available",
  "supported_endpoints": ["POST /v1/images/generations"]},
 {"id": "big-chat", "display_name": "Big Chat", "author": "acme", "category": "text", "is_featured": true, "status": "available",
  "features": ["reasoning", "function_calling"], "context_window": 1000000, "max_output_tokens": 131072,
  "supported_endpoints": ["POST /v1/chat/completions", "POST /v1/responses", "POST /v1/messages", "POST /v1beta/models/big-chat:generateContent"],
  "pricing": {"prompt": "0.0000014", "completion": "0.0000044", "input_cache_read": "0.00000014"}},
 {"id": "big-chat:variant1", "author": "acme", "category": "text", "is_featured": true, "status": "available",
  "features": ["function_calling"], "context_window": 1000000,
  "supported_endpoints": ["POST /v1/chat/completions", "POST /v1/messages"],
  "pricing": {"prompt": "0.0000001", "completion": "0.0000002"}},
 {"id": "acme-flash", "display_name": "Acme Flash", "author": "acme", "category": "text", "is_featured": true, "status": "available",
  "features": ["function_calling"], "context_window": 1000000, "aliases": ["acme/flash"],
  "supported_endpoints": ["POST /v1/chat/completions", "POST /v1/messages"],
  "pricing": {"prompt": "0.000000075", "completion": "0.00000025"}},
 {"id": "other-cheap", "author": "zeta", "category": "text", "is_featured": true, "status": "available",
  "features": ["function_calling"], "context_window": 1000000,
  "supported_endpoints": ["POST /v1/chat/completions", "POST /v1/messages"],
  "pricing": [{"prompt": "0.00000001", "completion": "0.00000002"}, {"prompt": "0.00000002", "completion": "0.00000004", "min_context": 32000}]},
 {"id": "tiny-window", "author": "zeta", "category": "text", "is_featured": true, "status": "available",
  "features": ["function_calling"], "context_window": 32000,
  "supported_endpoints": ["POST /v1/chat/completions"], "pricing": {"prompt": "0", "completion": "0"}},
 {"id": "no-tools", "category": "text", "is_featured": true, "status": "available",
  "supported_endpoints": ["POST /v1/chat/completions", "POST /v1/messages"]},
 {"id": "retired", "category": "text", "status": "unavailable", "features": ["function_calling"],
  "supported_endpoints": ["POST /v1/chat/completions"]}
]}`

func load(t *testing.T) *Catalog {
	t.Helper()
	var c Catalog
	if err := json.Unmarshal([]byte(fixture), &c); err != nil {
		t.Fatal(err)
	}
	return &c
}

func ids(models []Model) []string {
	var out []string
	for _, m := range models {
		out = append(out, m.ID)
	}
	return out
}

func TestForKeepsOnlyModelsAnAgentCanDrive(t *testing.T) {
	c := load(t)
	if got := ids(c.For(Messages)); len(got) != 4 || got[0] != "big-chat" {
		t.Fatalf("Messages: %v", got)
	}
	if got := ids(c.For(Responses)); len(got) != 1 || got[0] != "big-chat" {
		t.Fatalf("Responses: %v", got)
	}
	if got := ids(c.For(Gemini)); len(got) != 1 || got[0] != "big-chat" {
		t.Fatalf("Gemini: %v", got)
	}
	for _, m := range c.For(Chat) {
		if m.ID == "no-tools" || m.ID == "retired" || m.ID == "image-1" {
			t.Fatalf("Chat lists %s", m.ID)
		}
	}
}

func TestDefaultIsTheFirstFeaturedBaseModel(t *testing.T) {
	m, ok := load(t).Default(Messages)
	if !ok || m.ID != "big-chat" {
		t.Fatalf("got %v %v", m.ID, ok)
	}
}

func TestFastPrefersTheCheapestModelOfTheSameAuthor(t *testing.T) {
	c := load(t)
	main, _ := c.Find("big-chat")
	if fast := c.Fast(Messages, main); fast.ID != "acme-flash" {
		t.Fatalf("got %s, want acme-flash (same author, no variant)", fast.ID)
	}
	// A main model that is already the cheapest does its own background work.
	other, _ := c.Find("other-cheap")
	if fast := c.Fast(Chat, other); fast.ID != "other-cheap" {
		t.Fatalf("got %s, want other-cheap", fast.ID)
	}
	// No other model by the author: the cheapest featured base model overall,
	// never a variant, an unfeatured model or a window under the floor.
	var mixed Catalog
	if err := json.Unmarshal([]byte(`{"data": [
	 {"id": "solo-pro", "author": "solo", "category": "text", "is_featured": true, "status": "available", "features": ["function_calling"],
	  "context_window": 1000000, "supported_endpoints": ["POST /v1/chat/completions"], "pricing": {"prompt": "0.000003", "completion": "0.000015"}},
	 {"id": "solo-pro:cheap", "author": "solo", "category": "text", "is_featured": true, "status": "available", "features": ["function_calling"],
	  "context_window": 1000000, "supported_endpoints": ["POST /v1/chat/completions"], "pricing": {"prompt": "0", "completion": "0"}},
	 {"id": "small", "author": "x", "category": "text", "is_featured": true, "status": "available", "features": ["function_calling"],
	  "context_window": 32000, "supported_endpoints": ["POST /v1/chat/completions"], "pricing": {"prompt": "0", "completion": "0"}},
	 {"id": "hidden", "author": "y", "category": "text", "status": "available", "features": ["function_calling"],
	  "context_window": 1000000, "supported_endpoints": ["POST /v1/chat/completions"], "pricing": {"prompt": "0.0000001", "completion": "0.0000001"}},
	 {"id": "mid", "author": "y", "category": "text", "is_featured": true, "status": "available", "features": ["function_calling"],
	  "context_window": 200000, "supported_endpoints": ["POST /v1/chat/completions"], "pricing": {"prompt": "0.0000005", "completion": "0.000002"}}
	]}`), &mixed); err != nil {
		t.Fatal(err)
	}
	solo, _ := mixed.Find("solo-pro")
	if fast := mixed.Fast(Chat, solo); fast.ID != "mid" {
		t.Fatalf("got %s, want mid", fast.ID)
	}
	alone := &Catalog{Models: []Model{main}}
	if fast := alone.Fast(Messages, main); fast.ID != "big-chat" {
		t.Fatalf("a catalog of one falls back to main, got %s", fast.ID)
	}
}

func TestPriceReadsFlatAndTieredPricing(t *testing.T) {
	c := load(t)
	m, _ := c.Find("big-chat")
	p := m.Price()
	if !p.Known || p.Tiered || p.Input < 1.39 || p.Input > 1.41 || p.Output < 4.39 || p.CacheRead < 0.13 {
		t.Fatalf("flat: %+v", p)
	}
	m, _ = c.Find("other-cheap")
	if p := m.Price(); !p.Known || !p.Tiered || p.Input > 0.011 {
		t.Fatalf("tiered: %+v", p)
	}
	if p := (Model{}).Price(); p.Known {
		t.Fatal("no pricing must be unknown")
	}
}

func TestFindMatchesIdsAndAliasesIgnoringCase(t *testing.T) {
	c := load(t)
	if m, ok := c.Find("ACME/FLASH"); !ok || m.ID != "acme-flash" {
		t.Fatalf("alias: %v %v", m.ID, ok)
	}
	if _, ok := c.Find("missing"); ok {
		t.Fatal("found a missing model")
	}
	var nilCatalog *Catalog
	if _, ok := nilCatalog.Find("x"); ok {
		t.Fatal("nil catalog")
	}
}
