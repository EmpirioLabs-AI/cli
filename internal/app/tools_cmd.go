package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode"

	"golang.org/x/term"
)

// `empirio tools` lists the tools the signed-in account can run and shows one
// tool's arguments. The CLI keeps no list of its own: the MCP server's live
// tool list names the tools, their input schemas describe the arguments, and
// the server's public manifest groups them into toolsets.

const toolsUsage = "usage: empirio tools [<toolset> | <tool>] [--json]"

// findTool looks a tool up by name; a dash may stand for an underscore.
func findTool(tools []mcpTool, name string) (mcpTool, bool) {
	for _, alt := range []string{name, strings.ReplaceAll(name, "-", "_")} {
		for _, t := range tools {
			if t.Name == alt {
				return t, true
			}
		}
	}
	return mcpTool{}, false
}

func unknownTool(tools []mcpTool, name string) error {
	var near []string
	needle := strings.ToLower(strings.ReplaceAll(name, "-", "_"))
	for _, t := range tools {
		if needle != "" && strings.Contains(t.Name, needle) && len(near) < 6 {
			near = append(near, t.Name)
		}
	}
	if len(near) > 0 {
		return fmt.Errorf("unknown tool %q; did you mean %s? `empirio tools` lists every tool", name, strings.Join(near, ", "))
	}
	return fmt.Errorf("unknown tool %q; `empirio tools` lists the tools you can run", name)
}

// lookupToolset matches a toolset by its name; a dash may stand for an underscore.
func lookupToolset(m *manifest, name string) (toolset, bool) {
	if m == nil {
		return toolset{}, false
	}
	want := strings.ToLower(strings.ReplaceAll(name, "-", "_"))
	for _, t := range m.Toolsets {
		if t.Name == want {
			return t, true
		}
	}
	return toolset{}, false
}

// toolsCmd lists the tools the signed-in account can run, grouped by toolset,
// or shows one toolset or one tool's arguments.
func toolsCmd(args []string) (int, error) {
	asJSON, name := false, ""
	for _, a := range args {
		switch {
		case a == "--json":
			asJSON = true
		case a == "-h" || a == "--help":
			fmt.Fprintln(stdout, toolsUsage)
			return 0, nil
		case strings.HasPrefix(a, "-") || name != "":
			return 2, fmt.Errorf("unknown argument %q (%s)", a, toolsUsage)
		default:
			name = a
		}
	}
	manifests := make(chan *manifest, 1)
	go func() { manifests <- readManifest(mcpBase()) }()
	s, err := connect()
	if err != nil {
		return 1, err
	}
	tools, err := s.tools()
	if err != nil {
		return 1, err
	}
	m := <-manifests
	if name == "" {
		return 0, listTools(tools, m, nil, asJSON)
	}
	if set, ok := lookupToolset(m, name); ok {
		if err := listTools(tools, m, &set, asJSON); err != nil {
			return 1, err
		}
		if t, ok := findTool(tools, name); ok && !asJSON {
			fmt.Fprintln(stdout, dim(fmt.Sprintf("%s is also a tool: `empirio run %s --help` shows its arguments.", t.Name, t.Name)))
		}
		return 0, nil
	}
	t, ok := findTool(tools, name)
	if !ok {
		if m == nil {
			return 2, fmt.Errorf("%v (the toolsets could not be read from the server just now)", unknownTool(tools, name))
		}
		return 2, unknownTool(tools, name)
	}
	if asJSON {
		return 0, printJSON(toolRecord(t, toolsetOf(m, t.Name)))
	}
	return 0, describeTool(t)
}

func toolsetOf(m *manifest, tool string) string {
	if m == nil {
		return ""
	}
	for _, set := range m.Toolsets {
		if hasString(set.Tools, tool) {
			return set.Name
		}
	}
	return ""
}

// toolRecord is a tool's definition as the server sent it, with its toolset.
func toolRecord(t mcpTool, set string) map[string]any {
	var rec map[string]any
	if json.Unmarshal(t.raw, &rec) != nil || rec == nil {
		rec = map[string]any{"name": t.Name}
	}
	if set != "" {
		rec["toolset"] = set
	}
	return rec
}

func printJSON(v any) error {
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// listTools prints the tools, grouped by the manifest's toolsets when it was
// read, else as one list.
func listTools(tools []mcpTool, m *manifest, only *toolset, asJSON bool) error {
	type group struct {
		set   toolset
		tools []mcpTool
	}
	var groups []group
	if m != nil {
		placed := map[string]bool{}
		for _, set := range m.Toolsets {
			if only != nil && set.Name != only.Name {
				continue
			}
			g := group{set: set}
			for _, name := range set.Tools {
				if t, ok := findTool(tools, name); ok && t.Name == name && !placed[name] {
					g.tools = append(g.tools, t)
					placed[name] = true
				}
			}
			groups = append(groups, g)
		}
		if only == nil {
			other := group{set: toolset{Name: "other", Title: "Other"}}
			for _, t := range tools {
				if !placed[t.Name] {
					other.tools = append(other.tools, t)
				}
			}
			if len(other.tools) > 0 {
				groups = append(groups, other)
			}
		}
	} else {
		groups = []group{{tools: tools}}
	}
	if asJSON {
		var records []map[string]any
		for _, g := range groups {
			for _, t := range g.tools {
				rec := toolRecord(t, g.set.Name)
				if g.set.Name == "other" {
					delete(rec, "toolset")
				}
				records = append(records, rec)
			}
		}
		if records == nil {
			records = []map[string]any{}
		}
		return printJSON(map[string]any{"tools": records})
	}
	count := 0
	nameWidth := 0
	for _, g := range groups {
		for _, t := range g.tools {
			count++
			nameWidth = max(nameWidth, len(t.Name))
		}
	}
	if only != nil && count == 0 {
		return fmt.Errorf("this account has no %s tools", only.Name)
	}
	if count == 0 {
		return errors.New("the MCP server lists no tools for this account")
	}
	title := "EmpirioLabs AI tools you can run"
	if only != nil {
		title = firstNonEmpty(only.Title, only.Name) + " tools"
	}
	fmt.Fprintf(stdout, "%s (%d)\n", bold(title), count)
	width, onTerminal := terminalWidth()
	for _, g := range groups {
		if len(g.tools) == 0 {
			continue
		}
		fmt.Fprintln(stdout)
		if g.set.Name != "" {
			heading := bold(firstNonEmpty(g.set.Title, g.set.Name))
			if g.set.Name != "other" {
				heading += dim(" (" + g.set.Name + ")")
			}
			if g.set.Description != "" {
				heading += dim(": " + g.set.Description)
			}
			fmt.Fprintln(stdout, heading)
		}
		for _, t := range g.tools {
			line := firstSentence(firstNonEmpty(t.Description, t.title()))
			if room := width - nameWidth - 4; onTerminal && room >= 20 {
				line = truncate(line, room)
			}
			fmt.Fprintf(stdout, "  %-*s  %s\n", nameWidth, t.Name, printable(line))
		}
	}
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, dim("A tool's arguments: empirio tools <tool>    Run one: empirio run <tool> name=value ..."))
	return nil
}

// describeTool prints one tool and its arguments from its input schema.
func describeTool(t mcpTool) error {
	s, err := parseSchema(t.InputSchema)
	if err != nil {
		return fmt.Errorf("the arguments of %s could not be read: %w", t.Name, err)
	}
	head := bold(t.Name)
	if title := t.title(); title != "" {
		head += ": " + printable(title)
	}
	fmt.Fprintln(stdout, head)
	width, _ := terminalWidth()
	if t.Description != "" {
		fmt.Fprintln(stdout, wrap(printable(t.Description), width, ""))
	}
	fmt.Fprintln(stdout)
	if len(s.names) == 0 {
		fmt.Fprintln(stdout, "No arguments.")
	} else {
		fmt.Fprintln(stdout, bold("Arguments"))
		for _, n := range s.names {
			p := s.props[n]
			kind := typeLabel(p)
			if s.required(n) {
				kind += ", required"
			}
			fmt.Fprintf(stdout, "  %s  %s\n", n, dim(kind))
			text := ""
			if p != nil {
				text = strings.TrimSpace(strings.TrimSpace(p.Description) + " " + constraints(p))
			}
			if text != "" {
				fmt.Fprintln(stdout, wrap(printable(text), width, "      "))
			}
		}
	}
	example := "empirio run " + t.Name
	for _, n := range s.names {
		if s.required(n) {
			example += " " + n + "=<" + n + ">"
		}
	}
	if len(s.names) > len(s.Required) {
		example += " [name=value ...]"
	}
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, dim("Run it: "+example))
	return nil
}

func firstSentence(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if i := strings.Index(s, ". "); i > 0 {
		return s[:i+1]
	}
	return s
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimRight(string(r[:n-3]), " ,;:") + "..."
}

// wrap breaks text into lines of at most width columns, each after indent.
func wrap(text string, width int, indent string) string {
	limit := width - len(indent)
	if limit < 30 {
		limit = 30
	}
	var lines []string
	for _, para := range strings.Split(text, "\n") {
		line := ""
		for _, word := range strings.Fields(para) {
			if line != "" && len([]rune(line))+1+len([]rune(word)) > limit {
				lines = append(lines, indent+line)
				line = ""
			}
			if line == "" {
				line = word
			} else {
				line += " " + word
			}
		}
		lines = append(lines, indent+line)
	}
	return strings.Join(lines, "\n")
}

// terminalWidth is the width of the terminal stdout writes to, else 100 and false.
func terminalWidth() (int, bool) {
	if f, ok := stdout.(*os.File); ok && isTerminal(f) {
		if w, _, err := term.GetSize(int(f.Fd())); err == nil && w > 0 {
			return w, true
		}
	}
	return 100, false
}

// printable keeps a tool's text from steering the terminal: control
// characters (escape sequences among them) are dropped, except tab and newline.
func printable(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			return r
		}
		return -1
	}, s)
}
