package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// schema is the part of a tool's JSON Schema the CLI reads: enough to type
// name=value arguments, check them before a call and describe them.
type schema struct {
	Type        schemaTypes     `json:"type"`
	Description string          `json:"description"`
	Format      string          `json:"format"`
	Enum        []any           `json:"enum"`
	Default     any             `json:"default"`
	Minimum     *float64        `json:"minimum"`
	Maximum     *float64        `json:"maximum"`
	Items       *schema         `json:"items"`
	AnyOf       []*schema       `json:"anyOf"`
	OneOf       []*schema       `json:"oneOf"`
	Properties  json.RawMessage `json:"properties"`
	Required    []string        `json:"required"`
	Additional  json.RawMessage `json:"additionalProperties"`

	names []string // property names in the server's order
	props map[string]*schema
}

// schemaTypes is "type": one name or a list of them.
type schemaTypes []string

func (t *schemaTypes) UnmarshalJSON(b []byte) error {
	var one string
	if json.Unmarshal(b, &one) == nil {
		*t = schemaTypes{one}
		return nil
	}
	var many []string
	if json.Unmarshal(b, &many) == nil {
		*t = many
	}
	return nil // any other value leaves the property untyped
}

// parseSchema reads a tool's inputSchema with its properties in order.
func parseSchema(raw json.RawMessage) (*schema, error) {
	s := &schema{}
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return s, nil
	}
	if err := json.Unmarshal(raw, s); err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(s.Properties)) > 0 && string(bytes.TrimSpace(s.Properties)) != "null" {
		names, err := objectKeys(s.Properties)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(s.Properties, &s.props); err != nil {
			return nil, err
		}
		s.names = names
	}
	return s, nil
}

// objectKeys returns the keys of a JSON object in the order they appear.
func objectKeys(raw json.RawMessage) ([]string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("not a JSON object")
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := tok.(string)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, nil
}

// closed reports whether the schema refuses names it does not list.
func (s *schema) closed() bool { return string(bytes.TrimSpace(s.Additional)) == "false" }

func (s *schema) required(name string) bool {
	for _, r := range s.Required {
		if r == name {
			return true
		}
	}
	return false
}

// kinds lists the JSON types a value may have; empty means any.
func (s *schema) kinds() map[string]bool {
	k := map[string]bool{}
	untyped := false
	var walk func(x *schema, depth int)
	walk = func(x *schema, depth int) {
		if x == nil || depth > 8 {
			return
		}
		branches := append(append([]*schema{}, x.AnyOf...), x.OneOf...)
		if len(x.Type) == 0 && len(branches) == 0 {
			if len(x.Enum) == 0 {
				untyped = true
			}
			for _, v := range x.Enum {
				k[jsonKind(v)] = true
			}
		}
		for _, t := range x.Type {
			k[t] = true
		}
		for _, b := range branches {
			walk(b, depth+1)
		}
	}
	walk(s, 0)
	if untyped {
		return map[string]bool{}
	}
	return k
}

func jsonKind(v any) string {
	switch v.(type) {
	case string:
		return "string"
	case bool:
		return "boolean"
	case float64, json.Number:
		return "number"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	}
	return "null"
}

var (
	integerText = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)
	numberText  = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)
)

func parseBool(s string) (bool, bool) {
	switch strings.ToLower(s) {
	case "true", "yes", "1":
		return true, true
	case "false", "no", "0":
		return false, true
	}
	return false, false
}

// decodeJSON reads one JSON value, keeping numbers as written.
func decodeJSON(text string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, errors.New("more than one JSON value")
	}
	return v, nil
}

// typeValue turns the text of name=value into the value the property takes:
// numbers and booleans are parsed, lists and objects are read as JSON, and
// text stays as given. A list of strings also takes a single value.
func typeValue(name, text string, s *schema) (any, error) {
	kinds := map[string]bool{}
	if s != nil {
		kinds = s.kinds()
	}
	trimmed := strings.TrimSpace(text)
	if len(kinds) == 0 {
		if v, err := decodeJSON(trimmed); err == nil {
			return v, nil
		}
		return text, nil
	}
	if (kinds["object"] || kinds["array"]) && (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) {
		v, err := decodeJSON(trimmed)
		switch v.(type) {
		case map[string]any:
			if kinds["object"] {
				return v, nil
			}
		case []any:
			if kinds["array"] {
				return v, nil
			}
		}
		if !kinds["string"] {
			if err != nil {
				return nil, fmt.Errorf("%s takes %s, and %s is not valid JSON", name, article(typeLabel(s)), quoteShort(text))
			}
			return nil, fmt.Errorf("%s takes %s, not %s", name, article(typeLabel(s)), quoteShort(text))
		}
	}
	if kinds["integer"] && integerText.MatchString(trimmed) {
		return json.Number(trimmed), nil
	}
	if kinds["number"] && numberText.MatchString(trimmed) {
		return json.Number(trimmed), nil
	}
	if kinds["boolean"] {
		if b, ok := parseBool(trimmed); ok {
			return b, nil
		}
	}
	if kinds["null"] && trimmed == "null" && !kinds["string"] {
		return nil, nil
	}
	if kinds["string"] {
		if choices := s.stringEnum(); len(choices) > 0 && !hasString(choices, text) {
			return nil, fmt.Errorf("%s must be one of: %s", name, strings.Join(choices, ", "))
		}
		return text, nil
	}
	if kinds["array"] && s.Items != nil && s.Items.kinds()["string"] {
		return []any{text}, nil
	}
	return nil, fmt.Errorf("%s takes %s, not %s", name, article(typeLabel(s)), quoteShort(text))
}

// article puts "a" or "an" before a type label: "an integer", "a boolean".
func article(label string) string {
	if label == "any value" {
		return label
	}
	if strings.ContainsRune("aeiou", rune(label[0])) {
		return "an " + label
	}
	return "a " + label
}

func hasString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func quoteShort(s string) string {
	if r := []rune(s); len(r) > 60 {
		s = string(r[:57]) + "..."
	}
	return strconv.Quote(s)
}

// stringEnum is the property's list of allowed text values, when it has one.
func (s *schema) stringEnum() []string {
	if s == nil || len(s.Enum) == 0 {
		return nil
	}
	var out []string
	for _, v := range s.Enum {
		str, ok := v.(string)
		if !ok {
			return nil
		}
		out = append(out, str)
	}
	return out
}

// typeLabel names a property's type for people: "integer", "array of strings",
// "string or array of strings".
func typeLabel(s *schema) string {
	if s == nil {
		return "any value"
	}
	var parts []string
	add := func(label string) {
		if !hasString(parts, label) {
			parts = append(parts, label)
		}
	}
	var walk func(x *schema, depth int)
	walk = func(x *schema, depth int) {
		if x == nil || depth > 8 {
			return
		}
		for _, t := range x.Type {
			if t == "array" && x.Items != nil && len(x.Items.kinds()) > 0 {
				add("array of " + pluralKinds(typeLabel(x.Items)))
			} else {
				add(t)
			}
		}
		for _, b := range append(append([]*schema{}, x.AnyOf...), x.OneOf...) {
			walk(b, depth+1)
		}
	}
	walk(s, 0)
	if len(parts) == 0 {
		if k := s.kinds(); len(k) > 0 {
			for name := range k {
				parts = append(parts, name)
			}
			sort.Strings(parts)
		} else {
			return "any value"
		}
	}
	return strings.Join(parts, " or ")
}

func pluralKinds(label string) string {
	words := strings.Split(label, " or ")
	for i, w := range words {
		if !strings.Contains(w, " ") {
			words[i] = w + "s"
		}
	}
	return strings.Join(words, " or ")
}

// argumentList names a tool's arguments for an error message.
func argumentList(s *schema) string {
	if len(s.names) == 0 {
		return "no arguments"
	}
	parts := make([]string, len(s.names))
	for i, n := range s.names {
		parts[i] = n
		if s.required(n) {
			parts[i] += " (required)"
		}
	}
	return strings.Join(parts, ", ")
}

// constraints describes a property's allowed values, range and default.
func constraints(s *schema) string {
	var parts []string
	if choices := s.stringEnum(); len(choices) > 0 {
		parts = append(parts, "One of: "+strings.Join(choices, ", ")+".")
	}
	switch {
	case s.Minimum != nil && s.Maximum != nil:
		parts = append(parts, fmt.Sprintf("From %s to %s.", number(*s.Minimum), number(*s.Maximum)))
	case s.Minimum != nil:
		parts = append(parts, fmt.Sprintf("At least %s.", number(*s.Minimum)))
	case s.Maximum != nil:
		parts = append(parts, fmt.Sprintf("At most %s.", number(*s.Maximum)))
	}
	if s.Default != nil {
		if b, err := json.Marshal(s.Default); err == nil {
			parts = append(parts, "Default "+strings.Trim(string(b), `"`)+".")
		}
	}
	return strings.Join(parts, " ")
}

func number(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
