package agents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testMCP = "https://mcp.empiriolabs.ai/mcp/" + DefaultProfile

// served is what the server's manifest lists: its toolsets and its listed profiles.
var served = []string{"account", "agents", "batch", "cloud", "gpu", "hosted_agents", "inference", "jobs", "media", "models", "search", "text"}

func TestToolsURL(t *testing.T) {
	cases := map[string]string{
		"":              testMCP,
		"default":       testMCP,
		"all":           MCPBase,
		"none":          "",
		"off":           "",
		"search+media":  MCPBase + "/search+media",
		"Search + gpu":  MCPBase + "/search+gpu",
		"media+media":   MCPBase + "/media",
		"hosted_agents": MCPBase + "/hosted_agents",
		"inference+gpu": MCPBase + "/inference+gpu",
	}
	for in, want := range cases {
		got, err := ToolsURL(MCPBase, in, served)
		if err != nil || got != want {
			t.Errorf("ToolsURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"everything", "search+nope", "search+", "../x"} {
		if _, err := ToolsURL(MCPBase, bad, served); err == nil {
			t.Errorf("ToolsURL(%q) was accepted", bad)
		}
	}
	if _, err := ToolsURL(MCPBase, "nope", served); err == nil || !strings.Contains(err.Error(), "choose from account, agents") {
		t.Errorf("an unknown toolset should list the server's toolsets, got %v", err)
	}
}

// Without the server's list (it could not be read), a toolset the server
// added after this release still works, and a malformed name is still refused.
func TestToolsURLWithoutTheServersList(t *testing.T) {
	if got, err := ToolsURL(MCPBase, "brand_new+search", nil); err != nil || got != MCPBase+"/brand_new+search" {
		t.Errorf("ToolsURL(brand_new+search) = %q, %v", got, err)
	}
	for _, bad := range []string{"search+", "../x", "a/b", "UPPER$", strings.Repeat("a", 33)} {
		if _, err := ToolsURL(MCPBase, bad, nil); err == nil {
			t.Errorf("ToolsURL(%q) was accepted", bad)
		}
	}
	for in, want := range map[string]bool{"": false, "default": false, "ALL": false, "none": false, "off": false, "search": true, "gpu+media": true} {
		if got := NamesToolsets(in); got != want {
			t.Errorf("NamesToolsets(%q) = %v, want %v", in, got, want)
		}
	}
}

// planTools builds a plan with the MCP server set, in a temporary home with no
// system-defaults file of its own.
func planTools(t *testing.T, name, mcpURL string) (Plan, Context) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, v := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "GEMINI_CLI_HOME", "FACTORY_HOME"} {
		t.Setenv(v, "")
	}
	t.Setenv("GEMINI_CLI_SYSTEM_DEFAULTS_PATH", filepath.Join(home, "no-gemini-defaults.json"))
	t.Setenv("QWEN_CODE_SYSTEM_DEFAULTS_PATH", filepath.Join(home, "no-qwen-defaults.json"))
	c, _ := fixture(t)
	c.MCPURL = mcpURL
	agent, ok := Get(name)
	if !ok {
		t.Fatalf("no agent %s", name)
	}
	t.Chdir(home)
	p, err := agent.Plan(c, "/usr/bin/"+name, nil)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return p, c
}

func fileData(p Plan, contains string) []byte {
	for _, f := range append(append([]File{}, p.Files...), p.Persist...) {
		if strings.Contains(filepath.Base(f.Path), contains) {
			return f.Data
		}
	}
	return nil
}

func TestEveryToolsAgentGetsTheMCPServerOnlyWhenAsked(t *testing.T) {
	for _, a := range All() {
		t.Run(a.Name, func(t *testing.T) {
			with, _ := planTools(t, a.Name, testMCP)
			without, _ := planTools(t, a.Name, "")
			blob := func(p Plan) string {
				var b strings.Builder
				b.WriteString(strings.Join(p.Args, "\n"))
				for k, v := range p.Env {
					b.WriteString(k + "=" + v + "\n")
				}
				for _, f := range append(append([]File{}, p.Files...), p.Persist...) {
					b.Write(f.Data)
				}
				return b.String()
			}
			if got := strings.Contains(blob(with), testMCP); got != a.Tools {
				t.Errorf("Tools=%v but the plan names the MCP server: %v", a.Tools, got)
			}
			if strings.Contains(blob(without), "mcp.empiriolabs.ai") {
				t.Error("the MCP server is configured although the session asked for none")
			}
			for _, arg := range with.Args {
				if strings.Contains(arg, testKey) {
					t.Errorf("the key is on the command line: %q", arg)
				}
			}
		})
	}
}

func TestClaudeGetsTheServerThroughASessionMCPConfig(t *testing.T) {
	p, _ := planTools(t, "claude", testMCP)
	i := indexOf(p.Args, "--mcp-config")
	if i < 0 || i+1 >= len(p.Args) {
		t.Fatalf("no --mcp-config in %v", p.Args)
	}
	var cfg struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(fileData(p, "claude-mcp-"), &cfg); err != nil {
		t.Fatal(err)
	}
	s := cfg.MCPServers[MCPName]
	if s.Type != "http" || s.URL != testMCP || s.Headers["Authorization"] != "Bearer "+testKey {
		t.Errorf("server %+v", s)
	}
}

func TestCodexReadsTheBearerFromTheEnvironment(t *testing.T) {
	p, _ := planTools(t, "codex", testMCP)
	args := strings.Join(p.Args, " ")
	for _, want := range []string{`mcp_servers.empiriolabs.url="` + testMCP + `"`, `mcp_servers.empiriolabs.bearer_token_env_var="EMPIRIOLABS_API_KEY"`} {
		if !strings.Contains(args, want) {
			t.Errorf("missing %s in %s", want, args)
		}
	}
	if p.Env["EMPIRIOLABS_API_KEY"] != testKey {
		t.Error("EMPIRIOLABS_API_KEY is not set")
	}
}

func TestOpenCodeAndKiloGetARemoteServer(t *testing.T) {
	for name, env := range map[string]string{"opencode": "OPENCODE_CONFIG_CONTENT", "kilo": "KILO_CONFIG_CONTENT"} {
		p, _ := planTools(t, name, testMCP)
		var cfg struct {
			MCP map[string]struct {
				Type    string            `json:"type"`
				URL     string            `json:"url"`
				Enabled bool              `json:"enabled"`
				Headers map[string]string `json:"headers"`
			} `json:"mcp"`
		}
		if err := json.Unmarshal([]byte(p.Env[env]), &cfg); err != nil {
			t.Fatal(err)
		}
		s := cfg.MCP[MCPName]
		if s.Type != "remote" || s.URL != testMCP || !s.Enabled || s.Headers["Authorization"] != "Bearer {env:EMPIRIOLABS_API_KEY}" {
			t.Errorf("%s: server %+v", name, s)
		}
	}
}

func TestCrushNamesTheBearerByReference(t *testing.T) {
	p, _ := planTools(t, "crush", testMCP)
	data := fileData(p, "crush.json")
	if !strings.Contains(string(data), `"Authorization": "Bearer $EMPIRIOLABS_API_KEY"`) || strings.Contains(string(data), testKey) {
		t.Errorf("crush.json: %s", data)
	}
}

func TestQwenKeepsTheMachinesSystemDefaults(t *testing.T) {
	for name, env := range map[string]string{"qwen": "QWEN_CODE_SYSTEM_DEFAULTS_PATH"} {
		t.Run(name, func(t *testing.T) {
			p, c := planTools(t, name, testMCP)
			session := p.Env[env]
			if session == "" || filepath.Dir(session) != c.RunDir {
				t.Fatalf("%s=%q", env, session)
			}
			var cfg map[string]any
			if err := json.Unmarshal(fileData(p, name+"-defaults-"), &cfg); err != nil {
				t.Fatal(err)
			}
			s := cfg["mcpServers"].(map[string]any)[MCPName].(map[string]any)
			if s["httpUrl"] != testMCP || s["headers"].(map[string]any)["Authorization"] != "Bearer ${EMPIRIOLABS_API_KEY}" {
				t.Errorf("server %v", s)
			}
			if p.Env["EMPIRIOLABS_API_KEY"] != testKey {
				t.Error("EMPIRIOLABS_API_KEY is not set")
			}

			// An administrator's defaults file is copied in, and a server of
			// theirs with our name wins.
			dir := t.TempDir()
			machine := filepath.Join(dir, "system-defaults.json")
			os.WriteFile(machine, []byte(`{"ui":{"theme":"Admin"},"mcpServers":{"corp":{"httpUrl":"https://corp.example/mcp"},"empiriolabs":{"httpUrl":"https://corp.example/ours"}}}`), 0o600)
			t.Setenv(env, machine)
			env2, files, ok := mcpDefaults(c, env, dir, dir, dir, name)
			if !ok || env2[env] == "" {
				t.Fatal("no session defaults")
			}
			var merged map[string]any
			json.Unmarshal(files[0].Data, &merged)
			servers := merged["mcpServers"].(map[string]any)
			if merged["ui"].(map[string]any)["theme"] != "Admin" || servers["corp"] == nil ||
				servers[MCPName].(map[string]any)["httpUrl"] != "https://corp.example/ours" {
				t.Errorf("merged %v", merged)
			}

			// A defaults file that is not JSON: the session goes without the tools.
			os.WriteFile(machine, []byte("{ // comment\n}"), 0o600)
			if _, _, ok := mcpDefaults(c, env, dir, dir, dir, name); ok {
				t.Error("an unreadable defaults file was replaced")
			}
		})
	}
}

func indexOf(list []string, v string) int {
	for i, s := range list {
		if s == v {
			return i
		}
	}
	return -1
}
