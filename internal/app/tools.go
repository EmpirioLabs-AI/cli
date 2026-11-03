package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
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
	base := mcpBase()
	var served []string
	if agents.NamesToolsets(set) {
		served = serverToolsets(base)
	}
	return agents.ToolsURL(base, set, served)
}

// serverToolsets reads the toolset and profile names from the MCP server's
// public manifest. It returns nil when the manifest cannot be read, and the
// server then judges the names itself.
func serverToolsets(base string) []string {
	m := readManifest(base)
	if m == nil {
		return nil
	}
	names := make([]string, 0, len(m.Toolsets)+len(m.Profiles))
	seen := map[string]bool{}
	for _, t := range m.Toolsets {
		names = append(names, t.Name)
		seen[t.Name] = true
	}
	for _, p := range m.Profiles {
		if !seen[p] {
			names = append(names, p)
		}
	}
	sort.Strings(names)
	return names
}

// manifest is what the CLI reads from the MCP server's public manifest: the
// toolsets in the server's order, with the tools of each, and the names of
// its listed profiles.
type manifest struct {
	Toolsets []toolset
	Profiles []string
}

type toolset struct {
	Name, Title, Description string
	Tools                    []string
}

// readManifest reads /manifest.json on the MCP server's host, or returns nil.
func readManifest(base string) *manifest {
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
	var raw struct {
		Toolsets json.RawMessage            `json:"toolsets"`
		Profiles map[string]json.RawMessage `json:"profiles"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&raw); err != nil || len(raw.Toolsets) == 0 {
		return nil
	}
	order, err := objectKeys(raw.Toolsets)
	if err != nil || len(order) == 0 {
		return nil
	}
	var sets map[string]struct {
		Title       string   `json:"title"`
		Description string   `json:"description"`
		Tools       []string `json:"tools"`
	}
	if json.Unmarshal(raw.Toolsets, &sets) != nil {
		return nil
	}
	m := &manifest{}
	for _, name := range order {
		s := sets[name]
		m.Toolsets = append(m.Toolsets, toolset{Name: name, Title: s.Title, Description: s.Description, Tools: s.Tools})
	}
	for name := range raw.Profiles {
		m.Profiles = append(m.Profiles, name)
	}
	sort.Strings(m.Profiles)
	return m
}
