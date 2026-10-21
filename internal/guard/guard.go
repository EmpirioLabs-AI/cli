// Package guard puts back the few settings an agent saves on its own during a
// session, so a session started through empirio leaves the user's agent
// configuration exactly as it was.
//
// empirio passes everything to an agent through its process (environment,
// flags, a temporary settings file) and writes no agent file itself. Some
// agents still save a choice made inside the session, such as picking a model
// in Claude Code's /model, into the user's own config. A guard records exactly
// those keys before the agent starts and restores them after it exits; every
// other key, and every other edit the user makes meanwhile, is left alone.
package guard

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"reflect"
	"regexp"
	"strings"
)

// Guard records keys before a session and restores them after.
type Guard interface {
	Snapshot() error
	// Restore reverts the guarded keys and reports whether it rewrote the file.
	Restore() (bool, error)
	Describe() string
}

func readOptional(path string) ([]byte, bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}

func writeKeepMode(path string, data []byte) error {
	mode := fs.FileMode(0o600)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	tmp := path + ".empirio-restore"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// Home shortens a path under the home directory to ~ for messages.
func Home(path string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(path, home) {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}

// ── JSON: keys at any depth ─────────────────────────────────────────────────

// JSONKeys guards keys of a JSON object file. A key may be a dotted path
// ("security.auth.selectedType") into nested objects.
type JSONKeys struct {
	Path string
	Keys []string

	taken   bool
	existed bool
	orig    map[string]json.RawMessage
}

func (g *JSONKeys) Describe() string {
	return strings.Join(g.Keys, ", ") + " in " + Home(g.Path)
}

func (g *JSONKeys) Snapshot() error {
	b, ok, err := readOptional(g.Path)
	if err != nil {
		return err
	}
	g.taken, g.existed, g.orig = true, ok, map[string]json.RawMessage{}
	if !ok || len(bytes.TrimSpace(b)) == 0 {
		return nil
	}
	obj, err := parseObject(b)
	if err != nil {
		return fmt.Errorf("%s: %w", g.Path, err)
	}
	for _, k := range g.Keys {
		if v, ok := obj.lookup(strings.Split(k, ".")); ok {
			g.orig[k] = v
		}
	}
	return nil
}

func (g *JSONKeys) Restore() (bool, error) {
	if !g.taken {
		return false, nil
	}
	b, ok, err := readOptional(g.Path)
	if err != nil || !ok {
		return false, err
	}
	obj, err := parseObject(b)
	if err != nil {
		// Mid-edit or hand-broken: never rewrite a file we cannot read.
		return false, nil
	}
	changed := false
	for _, k := range g.Keys {
		path := strings.Split(k, ".")
		cur, hasCur := obj.lookup(path)
		orig, hasOrig := g.orig[k]
		switch {
		case hasOrig && (!hasCur || !sameJSON(cur, orig)):
			if obj.put(path, orig) {
				changed = true
			}
		case !hasOrig && hasCur:
			if obj.remove(path) {
				changed = true
			}
		}
	}
	if !changed {
		return false, nil
	}
	if !g.existed && len(obj.keys) == 0 {
		return true, os.Remove(g.Path)
	}
	return true, writeKeepMode(g.Path, obj.marshal())
}

func sameJSON(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return bytes.Equal(a, b)
	}
	return reflect.DeepEqual(x, y)
}

type object struct {
	keys []string
	vals map[string]json.RawMessage
}

func parseObject(b []byte) (*object, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return nil, errors.New("not a JSON object")
	}
	o := &object{vals: map[string]json.RawMessage{}}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		k, _ := tok.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		if _, dup := o.vals[k]; !dup {
			o.keys = append(o.keys, k)
		}
		o.vals[k] = v
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return o, nil
}

func (o *object) set(k string, v json.RawMessage) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *object) del(k string) {
	delete(o.vals, k)
	for i, x := range o.keys {
		if x == k {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			return
		}
	}
}

// lookup returns the raw value at a key path.
func (o *object) lookup(path []string) (json.RawMessage, bool) {
	v, ok := o.vals[path[0]]
	if !ok || len(path) == 1 {
		return v, ok
	}
	child, err := parseObject(v)
	if err != nil {
		return nil, false
	}
	return child.lookup(path[1:])
}

// put sets the value at a key path, creating objects on the way; it reports
// whether anything changed (a non-object in the way is left alone).
func (o *object) put(path []string, v json.RawMessage) bool {
	if len(path) == 1 {
		o.set(path[0], v)
		return true
	}
	child := &object{vals: map[string]json.RawMessage{}}
	if raw, ok := o.vals[path[0]]; ok {
		parsed, err := parseObject(raw)
		if err != nil {
			return false
		}
		child = parsed
	}
	if !child.put(path[1:], v) {
		return false
	}
	o.set(path[0], bytes.TrimSpace(child.marshal()))
	return true
}

// remove deletes the value at a key path and reports whether it was there.
func (o *object) remove(path []string) bool {
	if len(path) == 1 {
		if _, ok := o.vals[path[0]]; !ok {
			return false
		}
		o.del(path[0])
		return true
	}
	raw, ok := o.vals[path[0]]
	if !ok {
		return false
	}
	child, err := parseObject(raw)
	if err != nil || !child.remove(path[1:]) {
		return false
	}
	o.set(path[0], bytes.TrimSpace(child.marshal()))
	return true
}

// marshal writes two-space indented JSON, the layout the agents write themselves.
func (o *object) marshal() []byte {
	var b bytes.Buffer
	b.WriteString("{")
	for i, k := range o.keys {
		if i > 0 {
			b.WriteString(",")
		}
		kb, _ := json.Marshal(k)
		var v bytes.Buffer
		if json.Indent(&v, o.vals[k], "  ", "  ") != nil {
			v.Reset()
			v.Write(o.vals[k])
		}
		fmt.Fprintf(&b, "\n  %s: %s", kb, v.Bytes())
	}
	if len(o.keys) > 0 {
		b.WriteString("\n")
	}
	b.WriteString("}\n")
	return b.Bytes()
}

// ── TOML: bare keys in any table ────────────────────────────────────────────

// TOMLKeys guards `key = value` lines for bare key names in the root table and
// in every [table], editing lines in place so comments and layout survive.
type TOMLKeys struct {
	Path string
	Keys []string

	taken   bool
	existed bool
	orig    map[[2]string]string
}

var (
	tomlHeader = regexp.MustCompile(`^\s*\[\[?\s*([^\]]+?)\s*\]\]?\s*(#.*)?$`)
	tomlKey    = regexp.MustCompile(`^\s*([A-Za-z0-9_-]+)\s*=`)
)

func (g *TOMLKeys) Describe() string {
	return strings.Join(g.Keys, ", ") + " in " + Home(g.Path)
}

type tomlLine struct{ table, key string }

func (g *TOMLKeys) scan(lines []string) []tomlLine {
	out := make([]tomlLine, len(lines))
	table := ""
	for i, l := range lines {
		if m := tomlHeader.FindStringSubmatch(l); m != nil {
			table = m[1]
			out[i] = tomlLine{table: table}
			continue
		}
		out[i] = tomlLine{table: table}
		if m := tomlKey.FindStringSubmatch(l); m != nil {
			for _, k := range g.Keys {
				if m[1] == k {
					out[i].key = k
				}
			}
		}
	}
	return out
}

func splitLines(b []byte) []string {
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func (g *TOMLKeys) Snapshot() error {
	b, ok, err := readOptional(g.Path)
	if err != nil {
		return err
	}
	g.taken, g.existed, g.orig = true, ok, map[[2]string]string{}
	lines := splitLines(b)
	for i, info := range g.scan(lines) {
		if info.key != "" {
			g.orig[[2]string{info.table, info.key}] = lines[i]
		}
	}
	return nil
}

func (g *TOMLKeys) Restore() (bool, error) {
	if !g.taken {
		return false, nil
	}
	b, ok, err := readOptional(g.Path)
	if err != nil || !ok {
		return false, err
	}
	newline := "\n"
	if bytes.Contains(b, []byte("\r\n")) {
		newline = "\r\n"
	}
	lines := splitLines(b)
	info := g.scan(lines)
	seen := map[[2]string]bool{}
	changed := false
	var out []string
	for i, l := range lines {
		if info[i].key == "" {
			out = append(out, l)
			continue
		}
		id := [2]string{info[i].table, info[i].key}
		seen[id] = true
		orig, had := g.orig[id]
		switch {
		case !had:
			changed = true // added during the session
		case orig != l:
			out = append(out, orig)
			changed = true
		default:
			out = append(out, l)
		}
	}
	for id, line := range g.orig {
		if !seen[id] {
			out = insertInTable(out, id[0], line)
			changed = true
		}
	}
	if !changed {
		return false, nil
	}
	if !g.existed && strings.TrimSpace(strings.Join(out, "")) == "" {
		return true, os.Remove(g.Path)
	}
	return true, writeKeepMode(g.Path, []byte(strings.Join(out, newline)+newline))
}

// insertInTable puts line right after the header of table ("" is the root).
func insertInTable(lines []string, table, line string) []string {
	at := 0
	if table != "" {
		at = -1
		for i, l := range lines {
			if m := tomlHeader.FindStringSubmatch(l); m != nil && m[1] == table {
				at = i + 1
				break
			}
		}
		if at < 0 {
			return append(lines, "", "["+table+"]", line)
		}
	}
	out := append([]string{}, lines[:at]...)
	out = append(out, line)
	return append(out, lines[at:]...)
}
