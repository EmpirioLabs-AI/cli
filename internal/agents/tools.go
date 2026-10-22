package agents

import (
	"fmt"
	"regexp"
	"strings"
)

// MCPBase is the EmpirioLabs AI MCP server (https://docs.empiriolabs.ai/mcp).
const MCPBase = "https://mcp.empiriolabs.ai/mcp"

// DefaultProfile is what a session gets unless the user asks otherwise. The
// MCP server defines it beside its other profiles, in the API's OpenAPI
// document (x-mcp-profiles), the one source of the toolsets: the tools that
// run only when the agent calls them (web search, media generation, the
// catalog, jobs, the balance), without the paid infrastructure (GPU Cloud,
// hosted agents) that keeps billing after the session ends. Asking for it by
// name means a change there reaches every session without a CLI release.
const DefaultProfile = "coding-agents"

// toolsetName is the MCP server's own rule for a toolset or profile name.
var toolsetName = regexp.MustCompile(`^[a-z_-]{1,32}$`)

// ToolsURL resolves a --tools value against the MCP server's base URL:
// "" or "default" gives DefaultProfile, "all" every toolset, "none" (or
// "off") no MCP server at all (""), and otherwise toolsets joined by "+".
// served lists the toolsets and profiles the server publishes; nil (the list
// could not be read) passes any well-formed name on for the server to judge.
func ToolsURL(base, tools string, served []string) (string, error) {
	base = strings.TrimRight(base, "/")
	switch value := strings.ToLower(strings.TrimSpace(tools)); value {
	case "", "default":
		return base + "/" + DefaultProfile, nil
	case "all":
		return base, nil
	case "none", "off":
		return "", nil
	default:
		seen := map[string]bool{}
		var parts []string
		for _, part := range strings.Split(value, "+") {
			part = strings.TrimSpace(part)
			if !toolsetName.MatchString(part) || (served != nil && !contains(served, part)) {
				return "", unknownToolset(part, served)
			}
			if !seen[part] {
				seen[part] = true
				parts = append(parts, part)
			}
		}
		return base + "/" + strings.Join(parts, "+"), nil
	}
}

// NamesToolsets reports whether a --tools value names toolsets, which are
// checked against the server's list; default, all and none need no list.
func NamesToolsets(tools string) bool {
	switch strings.ToLower(strings.TrimSpace(tools)) {
	case "", "default", "all", "none", "off":
		return false
	}
	return true
}

func unknownToolset(name string, served []string) error {
	if len(served) == 0 {
		return fmt.Errorf("unknown toolset %q (use default, all, none, or toolsets joined by +; the toolsets: https://docs.empiriolabs.ai/cli#empiriolabs-ai-tools)", name)
	}
	return fmt.Errorf("unknown toolset %q (choose from %s, or default, all, none)", name, strings.Join(served, ", "))
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
