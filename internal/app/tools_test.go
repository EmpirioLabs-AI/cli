package app

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// The toolset names come from the MCP server's manifest, so a toolset the
// server adds is accepted without a CLI release.
func TestServerToolsetsReadsTheManifest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/manifest.json" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"toolsets": {"models": {}, "search": {}, "media": {}, "brand_new": {}},
		                 "profiles": {"media": ["models", "media"], "inference": ["models", "search"]}}`))
	}))
	defer srv.Close()
	got := serverToolsets(srv.URL + "/mcp")
	want := []string{"brand_new", "inference", "media", "models", "search"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("serverToolsets = %v, want %v", got, want)
	}
	t.Setenv("EMPIRIO_MCP_URL", srv.URL+"/mcp")
	t.Setenv("EMPIRIO_TOOLS", "")
	if u, err := toolsURL("brand_new+search"); err != nil || u != srv.URL+"/mcp/brand_new+search" {
		t.Errorf("toolsURL(brand_new+search) = %q, %v", u, err)
	}
	if _, err := toolsURL("nope"); err == nil || !strings.Contains(err.Error(), "choose from brand_new, inference") {
		t.Errorf("toolsURL(nope) should list the server's toolsets, got %v", err)
	}
	if u, err := toolsURL(""); err != nil || u != srv.URL+"/mcp/coding-agents" {
		t.Errorf("toolsURL() = %q, %v; want the server's coding-agents profile", u, err)
	}
}

func TestServerToolsetsGivesUpQuietly(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"missing":     func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) },
		"not JSON":    func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<html>")) },
		"no toolsets": func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"toolsets": {}}`)) },
	} {
		srv := httptest.NewServer(handler)
		if got := serverToolsets(srv.URL + "/mcp"); got != nil {
			t.Errorf("%s: serverToolsets = %v, want nil", name, got)
		}
		srv.Close()
	}
	if got := serverToolsets("::not a url"); got != nil {
		t.Errorf("a bad base gave %v", got)
	}
}
