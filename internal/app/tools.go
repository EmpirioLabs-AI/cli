package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/EmpirioLabs-AI/cli/internal/agents"
)

// toolsURL is the EmpirioLabs AI MCP server a session gets: --tools, else
// EMPIRIO_TOOLS, else the server's default profile. EMPIRIO_MCP_URL points the
// CLI at another MCP server for testing (https only, http on this computer; see
// checkOverrides). Toolsets named on the command line are checked against the
// ones the server publishes, so the CLI keeps no list of its own.
func toolsURL(flag string) (string, error) {
	set := flag
	if set == "" {
		set = os.Getenv("EMPIRIO_TOOLS")
	}
	base := strings.TrimSpace(os.Getenv("EMPIRIO_MCP_URL"))
	if base == "" {
		base = agents.MCPBase
	}
	var served []string
	if agents.NamesToolsets(set) {
		served = serverToolsets(base)
	}
	return agents.ToolsURL(base, set, served)
}

// serverToolsets reads the toolset and profile names from the MCP server's
// public manifest (/manifest.json on the server's host). It returns nil when
// the manifest cannot be read, and the server then judges the names itself.
func serverToolsets(base string) []string {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return nil
	}
	u.Path, u.RawPath, u.RawQuery, u.Fragment = "/manifest.json", "", "", ""
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", "empirio-cli/"+Version)
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var manifest struct {
		Toolsets map[string]json.RawMessage `json:"toolsets"`
		Profiles map[string]json.RawMessage `json:"profiles"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&manifest); err != nil || len(manifest.Toolsets) == 0 {
		return nil
	}
	names := make([]string, 0, len(manifest.Toolsets)+len(manifest.Profiles))
	for name := range manifest.Toolsets {
		names = append(names, name)
	}
	for name := range manifest.Profiles {
		if _, dup := manifest.Toolsets[name]; !dup {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
