package guard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestJSONRestoresOnlyTheGuardedKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	write(t, path, "{\n  \"model\": \"opus\",\n  \"theme\": \"dark\"\n}\n")
	g := &JSONKeys{Path: path, Keys: []string{"model"}}
	if err := g.Snapshot(); err != nil {
		t.Fatal(err)
	}
	// The agent saves a model; the user changes their theme meanwhile.
	write(t, path, "{\n  \"model\": \"glm-5-3\",\n  \"theme\": \"light\",\n  \"new\": 1\n}\n")
	changed, err := g.Restore()
	if err != nil || !changed {
		t.Fatalf("restore: %v %v", changed, err)
	}
	got := read(t, path)
	if !strings.Contains(got, `"model": "opus"`) || !strings.Contains(got, `"theme": "light"`) || !strings.Contains(got, `"new": 1`) {
		t.Fatalf("got %s", got)
	}
}

func TestJSONRemovesAKeyAddedDuringTheSessionAndAFileItCreated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	g := &JSONKeys{Path: path, Keys: []string{"model"}}
	if err := g.Snapshot(); err != nil {
		t.Fatal(err)
	}
	write(t, path, `{"model": "glm-5-3"}`)
	if changed, err := g.Restore(); err != nil || !changed {
		t.Fatalf("%v %v", changed, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("a file the session created with only the guarded key should be gone")
	}
}

func TestJSONNestedKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	write(t, path, `{"security": {"auth": {"selectedType": "oauth-personal"}, "folderTrust": {"enabled": true}}, "ui": {"theme": "x"}}`)
	g := &JSONKeys{Path: path, Keys: []string{"security.auth.selectedType", "model.name"}}
	if err := g.Snapshot(); err != nil {
		t.Fatal(err)
	}
	write(t, path, `{"security": {"auth": {"selectedType": "gemini-api-key"}, "folderTrust": {"enabled": true}}, "ui": {"theme": "dark"}, "model": {"name": "glm-5-3"}}`)
	if changed, err := g.Restore(); err != nil || !changed {
		t.Fatalf("%v %v", changed, err)
	}
	got := read(t, path)
	for _, want := range []string{`"selectedType": "oauth-personal"`, `"theme": "dark"`, `"enabled": true`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in %s", want, got)
		}
	}
	if strings.Contains(got, "glm-5-3") {
		t.Fatalf("model.name was added during the session and must go: %s", got)
	}
}

func TestJSONLeavesAnUnreadableFileAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	write(t, path, `{"model": "opus"}`)
	g := &JSONKeys{Path: path, Keys: []string{"model"}}
	if err := g.Snapshot(); err != nil {
		t.Fatal(err)
	}
	write(t, path, `{"model": "glm`)
	if changed, err := g.Restore(); err != nil || changed {
		t.Fatalf("%v %v", changed, err)
	}
	if read(t, path) != `{"model": "glm` {
		t.Fatal("a broken file must not be rewritten")
	}
}

func TestTOMLRestoresKeysInTheRootAndInTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	write(t, path, "# mine\r\nmodel = \"gpt-5\"\r\n\r\n[profiles.work]\r\nmodel_reasoning_effort = \"high\"\r\n")
	g := &TOMLKeys{Path: path, Keys: []string{"model", "model_reasoning_effort"}}
	if err := g.Snapshot(); err != nil {
		t.Fatal(err)
	}
	write(t, path, "# mine\r\nmodel = \"glm-5-3\"\r\nmodel_reasoning_effort = \"low\"\r\n\r\n[profiles.work]\r\n")
	if changed, err := g.Restore(); err != nil || !changed {
		t.Fatalf("%v %v", changed, err)
	}
	got := read(t, path)
	want := "# mine\r\nmodel = \"gpt-5\"\r\n\r\n[profiles.work]\r\nmodel_reasoning_effort = \"high\"\r\n"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestNothingChangedMeansNothingWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	write(t, path, "model = \"gpt-5\"\n")
	g := &TOMLKeys{Path: path, Keys: []string{"model"}}
	if err := g.Snapshot(); err != nil {
		t.Fatal(err)
	}
	if changed, err := g.Restore(); err != nil || changed {
		t.Fatalf("%v %v", changed, err)
	}
}
