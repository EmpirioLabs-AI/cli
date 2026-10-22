package app

import (
	"os"
	"strings"

	"github.com/EmpirioLabs-AI/cli/internal/agents"
)

// toolsURL is the EmpirioLabs AI MCP server a session gets: --tools, else
// EMPIRIO_TOOLS, else the default toolsets. EMPIRIO_MCP_URL points the CLI at
// another MCP server for testing (https only, http on this computer; see
// checkOverrides).
func toolsURL(flag string) (string, error) {
	set := flag
	if set == "" {
		set = os.Getenv("EMPIRIO_TOOLS")
	}
	base := strings.TrimSpace(os.Getenv("EMPIRIO_MCP_URL"))
	if base == "" {
		base = agents.MCPBase
	}
	return agents.ToolsURL(base, set)
}
