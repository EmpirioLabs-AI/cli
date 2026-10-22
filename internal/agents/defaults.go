package agents

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// Qwen Code reads a "system defaults" settings file below every other layer
// (user, project and the administrator's system settings all outrank it), and
// takes its path from an environment variable. Pointing that variable at a
// session copy adds the EmpirioLabs AI MCP server for one session (Gemini CLI
// has the same layer but refuses a file in a directory an administrator does
// not own, so it gets no tools; see gemini.go):
// MCP servers merge by name, so the user's own servers stay, and a server the
// user already calls "empiriolabs" wins over ours. The machine's own defaults
// file, when there is one, is copied in unchanged, so nothing an administrator
// set is dropped; when it cannot be read as JSON the session goes without the
// tools rather than without those defaults.
func mcpDefaults(c Context, pathEnv, dirWindows, dirMac, dirLinux, prefix string) (env map[string]string, files []File, ok bool) {
	if c.MCPURL == "" {
		return nil, nil, false
	}
	machine := os.Getenv(pathEnv)
	if machine == "" {
		dir := dirLinux
		switch runtime.GOOS {
		case "windows":
			dir = dirWindows
		case "darwin":
			dir = dirMac
		}
		machine = filepath.Join(dir, "system-defaults.json")
	}
	settings := map[string]any{}
	if data, err := os.ReadFile(machine); err == nil {
		if json.Unmarshal(data, &settings) != nil {
			return nil, nil, false
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, false
	}
	servers, _ := settings["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	if _, taken := servers[MCPName]; !taken {
		servers[MCPName] = map[string]any{
			"httpUrl": c.MCPURL,
			// Both CLIs expand ${VAR} in settings; the key stays in the environment.
			"headers": map[string]string{"Authorization": "Bearer ${EMPIRIOLABS_API_KEY}"},
		}
	}
	settings["mcpServers"] = servers
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, nil, false
	}
	path := filepath.Join(c.RunDir, prefix+"-defaults-"+sessionID()+".json")
	return map[string]string{pathEnv: path, "EMPIRIOLABS_API_KEY": c.Key}, []File{{Path: path, Data: data}}, true
}
