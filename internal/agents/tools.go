package agents

import (
	"fmt"
	"strings"
)

// MCPBase is the EmpirioLabs AI MCP server (https://docs.empiriolabs.ai/mcp).
const MCPBase = "https://mcp.empiriolabs.ai/mcp"

// DefaultToolsets is what a session gets unless the user asks otherwise:
// everything that runs only when the agent asks for it (web search, media
// generation, the catalog, jobs, the balance), without the paid infrastructure
// (GPU Cloud, hosted agents) that keeps billing after the session ends.
const DefaultToolsets = "models+search+media+jobs+account"

// Toolsets lists the MCP server's toolsets, the values --tools accepts.
var Toolsets = []string{"models", "text", "search", "media", "jobs", "agents", "account", "batch", "gpu", "hosted_agents"}

// ToolsURL resolves a --tools value against the MCP server's base URL:
// "" or "default" gives DefaultToolsets, "all" every toolset, "none" (or
// "off") no MCP server at all (""), and otherwise toolsets joined by "+".
func ToolsURL(base, tools string) (string, error) {
	base = strings.TrimRight(base, "/")
	switch value := strings.ToLower(strings.TrimSpace(tools)); value {
	case "", "default":
		return base + "/" + DefaultToolsets, nil
	case "all":
		return base, nil
	case "none", "off":
		return "", nil
	default:
		seen := map[string]bool{}
		var parts []string
		for _, part := range strings.Split(value, "+") {
			part = strings.TrimSpace(part)
			if !known(part) {
				return "", fmt.Errorf("unknown toolset %q (choose from %s, or default, all, none)", part, strings.Join(Toolsets, ", "))
			}
			if !seen[part] {
				seen[part] = true
				parts = append(parts, part)
			}
		}
		return base + "/" + strings.Join(parts, "+"), nil
	}
}

func known(toolset string) bool {
	for _, t := range Toolsets {
		if t == toolset {
			return true
		}
	}
	return false
}
