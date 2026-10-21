package agents

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EmpirioLabs-AI/cli/internal/catalog"
)

const testKey = "sk-empiriolabs-TESTKEYTESTKEYTESTKEYTESTKEY0123456789"

func fixture(t *testing.T) (Context, *catalog.Catalog) {
	t.Helper()
	raw := `{"data": [
	 {"id": "glm-5-3", "display_name": "GLM 5.3", "author": "zhipu", "category": "text", "is_featured": true, "status": "available",
	  "features": ["reasoning", "function_calling"], "context_window": 1000000, "max_output_tokens": 131072,
	  "capabilities": {"reasoning": true},
	  "supported_endpoints": ["POST /v1/chat/completions", "POST /v1/responses", "POST /v1/messages", "POST /v1beta/models/glm-5-3:generateContent"],
	  "pricing": {"prompt": "0.0000014", "completion": "0.0000044", "input_cache_read": "0.0000014"},
	  "supported_parameters": [{"name": "reasoning_effort", "type": "enum", "options": ["low", "high", "max"], "default": "max"}]},
	 {"id": "glm-5-3-flash", "display_name": "GLM 5.3 Flash", "author": "zhipu", "category": "text", "is_featured": true, "status": "available",
	  "features": ["function_calling"], "context_window": 1000000, "max_output_tokens": 131072, "input_modalities": ["text", "image"],
	  "supported_endpoints": ["POST /v1/chat/completions", "POST /v1/responses", "POST /v1/messages", "POST /v1beta/models/glm-5-3-flash:generateContent"],
	  "pricing": {"prompt": "0.000000075", "completion": "0.00000025"}}
	]}`
	var cat catalog.Catalog
	if err := json.Unmarshal([]byte(raw), &cat); err != nil {
		t.Fatal(err)
	}
	main, _ := cat.Find("glm-5-3")
	return Context{Key: testKey, BaseURL: "https://api.empiriolabs.ai", Main: main, Fast: cat.Fast(catalog.Chat, main), Catalog: &cat, Version: "test", RunDir: t.TempDir()}, &cat
}

// planFor builds an agent's plan in a temporary home so guards never point at the real one.
func planFor(t *testing.T, name string, args ...string) Plan {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, v := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "GEMINI_CLI_HOME", "FACTORY_HOME"} {
		t.Setenv(v, "")
	}
	c, _ := fixture(t)
	agent, ok := Get(name)
	if !ok {
		t.Fatalf("no agent %s", name)
	}
	t.Chdir(home)
	p, err := agent.Plan(c, "/usr/bin/"+name, args)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return p
}

func TestNoAgentEverGetsTheKeyOnItsCommandLine(t *testing.T) {
	for _, a := range All() {
		t.Run(a.Name, func(t *testing.T) {
			p := planFor(t, a.Name)
			for _, arg := range p.Args {
				if strings.Contains(arg, testKey) {
					t.Fatalf("the key is in argv: %q", arg)
				}
			}
			found := false
			for _, v := range p.Env {
				if strings.Contains(v, testKey) {
					found = true
				}
			}
			for _, f := range p.Files {
				if strings.Contains(string(f.Data), testKey) {
					found = true
				}
			}
			for _, f := range p.Persist {
				if strings.Contains(string(f.Data), testKey) {
					t.Fatal("a launcher file that outlives the session holds the key")
				}
			}
			if !found {
				t.Fatal("the key reaches the agent neither by environment nor by a session file")
			}
			if len(p.Secrets) == 0 || p.Secrets[0] != testKey {
				t.Fatal("the key is not marked secret for --print-env")
			}
			if p.Summary == "" || p.Binary == "" {
				t.Fatal("empty summary or binary")
			}
		})
	}
}

func TestSessionFilesStayInTheRunDirectory(t *testing.T) {
	for _, a := range All() {
		p := planFor(t, a.Name)
		for _, f := range p.Files {
			if !strings.Contains(f.Path, string(filepath.Separator)) {
				t.Fatalf("%s: relative session file %s", a.Name, f.Path)
			}
		}
	}
}

func TestClaudeSettingsOutrankTheUsersAndPointAtTheAPI(t *testing.T) {
	p := planFor(t, "claude", "-p", "hi")
	if p.Args[0] != "--settings" || p.Args[len(p.Args)-2] != "-p" {
		t.Fatalf("args: %v", p.Args)
	}
	var s struct {
		Env         map[string]string       `json:"env"`
		Model       string                  `json:"model"`
		Permissions struct{ Deny []string } `json:"permissions"`
		ModelPicker struct {
			Options []map[string]string `json:"options"`
			Replace bool                `json:"replaceBuiltInOptions"`
		} `json:"modelPicker"`
	}
	if err := json.Unmarshal(p.Files[0].Data, &s); err != nil {
		t.Fatal(err)
	}
	if s.Env["ANTHROPIC_BASE_URL"] != "https://api.empiriolabs.ai" || s.Env["ANTHROPIC_AUTH_TOKEN"] != testKey || s.Env["ANTHROPIC_API_KEY"] != "" {
		t.Fatalf("env: %v", s.Env)
	}
	if s.Model != "glm-5-3" || s.Env["ANTHROPIC_DEFAULT_HAIKU_MODEL"] != "glm-5-3-flash" {
		t.Fatalf("models: %s %s", s.Model, s.Env["ANTHROPIC_DEFAULT_HAIKU_MODEL"])
	}
	if s.Env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"] != "200000" || s.Env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"] != "1000000" {
		t.Fatalf("windows: %v", s.Env)
	}
	if len(s.Permissions.Deny) != 1 || s.Permissions.Deny[0] != "WebSearch" {
		t.Fatal("WebSearch must be denied")
	}
	if !s.ModelPicker.Replace || len(s.ModelPicker.Options) != 2 || s.ModelPicker.Options[0]["model"] != "glm-5-3" {
		t.Fatalf("picker: %+v", s.ModelPicker)
	}
	if _, ok := Get("claude"); !ok {
		t.Fatal("lookup by name")
	}
	if a, _ := Get("claude-code"); a.Name != "claude" {
		t.Fatal("alias claude-code")
	}
}

func TestClaudeRefusesASecondSettingsFlag(t *testing.T) {
	c, _ := fixture(t)
	a, _ := Get("claude")
	if _, err := a.Plan(c, "claude", []string{"--settings", "mine.json"}); err == nil {
		t.Fatal("expected a refusal")
	}
}

func TestCodexOverridesNameTheProviderAndKeepTheKeyInTheEnvironment(t *testing.T) {
	p := planFor(t, "codex", "exec", "hi")
	joined := strings.Join(p.Args, " ")
	for _, want := range []string{
		`model_provider="empiriolabs"`,
		`model_providers.empiriolabs.base_url="https://api.empiriolabs.ai/v1"`,
		`model_providers.empiriolabs.wire_api="responses"`,
		`model_providers.empiriolabs.env_key="EMPIRIOLABS_API_KEY"`,
		`model="glm-5-3"`,
		"model_context_window=1000000",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %s in %s", want, joined)
		}
	}
	if p.Env["EMPIRIOLABS_API_KEY"] != testKey || p.Args[len(p.Args)-2] != "exec" {
		t.Fatal("env or passthrough")
	}
	c, _ := fixture(t)
	a, _ := Get("codex")
	for _, flag := range []string{"--profile", "-p", "--oss"} {
		if _, err := a.Plan(c, "codex", []string{flag, "x"}); err == nil {
			t.Fatalf("%s must be refused", flag)
		}
	}
}

func TestOpenCodeAndKiloInlineConfig(t *testing.T) {
	for _, name := range []string{"opencode", "kilo"} {
		p := planFor(t, name)
		env := "OPENCODE_CONFIG_CONTENT"
		if name == "kilo" {
			env = "KILO_CONFIG_CONTENT"
		}
		var cfg struct {
			Model      string `json:"model"`
			SmallModel string `json:"small_model"`
			Provider   map[string]struct {
				Options map[string]string         `json:"options"`
				Models  map[string]map[string]any `json:"models"`
			} `json:"provider"`
		}
		if err := json.Unmarshal([]byte(p.Env[env]), &cfg); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if cfg.Model != "empiriolabs/glm-5-3" || cfg.SmallModel != "empiriolabs/glm-5-3-flash" {
			t.Fatalf("%s: models %s %s", name, cfg.Model, cfg.SmallModel)
		}
		opts := cfg.Provider["empiriolabs"].Options
		if opts["apiKey"] != "{env:EMPIRIOLABS_API_KEY}" || opts["baseURL"] != "https://api.empiriolabs.ai/v1" {
			t.Fatalf("%s: options %v", name, opts)
		}
		if strings.Contains(p.Env[env], testKey) {
			t.Fatal("the inline config names the key by reference only")
		}
		if len(p.Env[env]) > 30_000 {
			t.Fatalf("%s: inline config of %d bytes risks the Windows environment limit", name, len(p.Env[env]))
		}
	}
}

func TestGeminiSelectsTheKeyLoginAndRestartsItself(t *testing.T) {
	p := planFor(t, "gemini")
	if p.Env["GEMINI_CLI_AUTH_OVERRIDE"] != "gemini-api-key" || p.Env["GOOGLE_GEMINI_BASE_URL"] != "https://api.empiriolabs.ai" {
		t.Fatalf("env: %v", p.Env)
	}
	if len(p.Relaunch) != 1 || p.Relaunch[0] != 199 {
		t.Fatal("relaunch on 199")
	}
	if !strings.Contains(string(p.Files[0].Data), "google_web_search") {
		t.Fatal("policy file")
	}
	found := false
	for _, u := range p.Unset {
		if u == "GOOGLE_API_KEY" {
			found = true
		}
	}
	if !found {
		t.Fatal("GOOGLE_API_KEY must not reach the session")
	}
}

func TestAiderSessionFilesAndFlags(t *testing.T) {
	p := planFor(t, "aider")
	joined := strings.Join(p.Args, " ")
	if !strings.Contains(joined, "--openai-api-base https://api.empiriolabs.ai/v1") || !strings.Contains(joined, "--model openai/glm-5-3") {
		t.Fatalf("args: %s", joined)
	}
	settings := string(p.Files[1].Data)
	if !strings.Contains(settings, "aider/extra_params") || !strings.Contains(settings, "weak_model_name: openai/glm-5-3-flash") {
		t.Fatalf("settings: %s", settings)
	}
	var meta map[string]map[string]any
	if err := json.Unmarshal(p.Files[0].Data, &meta); err != nil || meta["openai/glm-5-3"]["max_input_tokens"].(float64) != 1000000 {
		t.Fatalf("metadata: %v %v", err, meta)
	}
}

func TestGooseStartsASessionByDefault(t *testing.T) {
	p := planFor(t, "goose")
	if len(p.Args) != 1 || p.Args[0] != "session" || p.Env["GOOSE_PROVIDER"] != "empiriolabs" || p.Env["GOOSE_MODEL"] != "glm-5-3" {
		t.Fatalf("plan: %v %v", p.Args, p.Env)
	}
	p = planFor(t, "goose", "run", "-t", "hi")
	if p.Args[0] != "run" {
		t.Fatal("arguments pass through")
	}
}

func TestOpenHandsAlwaysSetsAllThree(t *testing.T) {
	p := planFor(t, "openhands")
	if p.Args[0] != "--override-with-envs" || p.Env["LLM_BASE_URL"] != "https://api.empiriolabs.ai/v1" || p.Env["LLM_MODEL"] != "openai/glm-5-3" {
		t.Fatalf("plan: %v %v", p.Args, p.Env)
	}
}

func TestCrushWritesItsLauncherConfigWithoutTheKey(t *testing.T) {
	p := planFor(t, "crush")
	if len(p.Persist) != 1 || !strings.HasSuffix(p.Persist[0].Path, "crush.json") {
		t.Fatalf("persist: %v", p.Persist)
	}
	var cfg struct {
		Providers map[string]struct {
			APIKey string           `json:"api_key"`
			Models []map[string]any `json:"models"`
		} `json:"providers"`
		Models map[string]map[string]any `json:"models"`
	}
	if err := json.Unmarshal(p.Persist[0].Data, &cfg); err != nil {
		t.Fatal(err)
	}
	prov := cfg.Providers["empiriolabs-ai"]
	if prov.APIKey != "$EMPIRIOLABS_API_KEY" || len(prov.Models) != 2 || cfg.Models["large"]["model"] != "glm-5-3" {
		t.Fatalf("config: %+v", cfg)
	}
	if levels := prov.Models[0]["reasoning_levels"]; levels == nil {
		t.Fatal("reasoning levels from the catalog")
	}
	if p.Args[0] != "--data-dir" {
		t.Fatalf("args: %v", p.Args)
	}
}

func TestDroidListsCustomModelsAndPicksTheMain(t *testing.T) {
	p := planFor(t, "droid", "exec", "hi")
	if p.Args[0] != "exec" || p.Args[1] != "--settings" {
		t.Fatalf("args: %v", p.Args)
	}
	var s struct {
		Model        string           `json:"model"`
		CustomModels []map[string]any `json:"customModels"`
	}
	if err := json.Unmarshal(p.Files[0].Data, &s); err != nil {
		t.Fatal(err)
	}
	if s.Model != "custom:empiriolabs-glm-5-3" || s.CustomModels[0]["apiKey"] != "${EMPIRIOLABS_API_KEY}" {
		t.Fatalf("settings: %+v", s)
	}
}

func TestTokensAndMoney(t *testing.T) {
	cases := map[int]string{1_000_000: "1M", 1_048_576: "1M", 2_000_000: "2M", 1_500_000: "1.5M", 262_144: "262K", 128_000: "128K", 500: "500"}
	for in, want := range cases {
		if got := Tokens(in); got != want {
			t.Fatalf("Tokens(%d) = %s, want %s", in, got, want)
		}
	}
	for in, want := range map[float64]string{1.4: "1.40", 0.075: "0.075", 15: "15.00", 0.25: "0.25", 4.951: "4.951"} {
		if got := Money(in); got != want {
			t.Fatalf("Money(%v) = %s, want %s", in, got, want)
		}
	}
}
