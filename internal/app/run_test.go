package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EmpirioLabs-AI/cli/internal/config"
)

// testTools is a tools/list the way the EmpirioLabs AI MCP server writes it.
const testTools = `[
 {"name": "list_models", "title": "List models", "description": "List models with prices. Filter them.",
  "inputSchema": {"type": "object", "properties": {
    "modality": {"type": "string", "enum": ["text", "image"], "description": "Filter by modality."},
    "limit": {"type": "integer", "minimum": 1, "maximum": 200, "description": "Maximum rows."}},
   "additionalProperties": false},
  "annotations": {"readOnlyHint": true}},
 {"name": "generate_image", "title": "Generate an image", "description": "Generate images from a prompt. Uses credits.",
  "inputSchema": {"type": "object", "properties": {
    "model": {"type": "string", "description": "Image model id."},
    "prompt": {"type": "string", "description": "Description of the image."},
    "num_images": {"type": "integer", "minimum": 1, "maximum": 4, "default": 1, "description": "Number of images."},
    "image": {"type": "array", "items": {"type": "string", "format": "uri"}, "description": "Reference image URLs."},
    "parameters": {"type": "object", "additionalProperties": true, "description": "Extra request fields."},
    "wait_seconds": {"type": "integer", "minimum": 0, "maximum": 50, "description": "Seconds to wait."}},
   "required": ["prompt"], "additionalProperties": false},
  "annotations": {"readOnlyHint": false}},
 {"name": "get_job", "title": "Get a job", "description": "Get a job by job_id.",
  "inputSchema": {"type": "object", "properties": {
    "job_id": {"type": "string", "description": "Job id."},
    "wait_seconds": {"type": "integer", "minimum": 0, "maximum": 50}},
   "required": ["job_id"], "additionalProperties": false},
  "annotations": {"readOnlyHint": true}},
 {"name": "search", "title": "Search", "description": "Search the catalog and documentation.",
  "inputSchema": {"type": "object", "properties": {"query": {"type": "string"}}, "required": ["query"], "additionalProperties": false},
  "annotations": {"readOnlyHint": true}},
 {"name": "mystery_tool", "description": "A tool no toolset lists.", "inputSchema": {"type": "object"}}
]`

const testManifest = `{"toolsets": {
  "models": {"title": "Models", "description": "Browse the catalog", "tools": ["list_models", "search"]},
  "media": {"title": "Media", "description": "Generate media (uses credits)", "tools": ["generate_image"]},
  "search": {"title": "Search", "description": "Search the web", "tools": []},
  "jobs": {"title": "Jobs", "description": "Check jobs", "tools": ["get_job"]}},
 "profiles": {"inference": ["models", "search"]}}`

type mcpCall struct {
	Method string
	Name   string
	Args   map[string]any
	Header http.Header
}

// fakeMCP is an MCP server for tests: the handshake, tools/list from tools,
// and tools/call through reply.
type fakeMCP struct {
	*httptest.Server
	tools    string
	manifest string
	sse      bool
	status   int    // answer every POST with this status instead
	redirect string // send every POST here instead
	rpcError string // answer tools/call with this JSON-RPC error
	reply    func(name string, args map[string]any) string

	mu    sync.Mutex
	calls []mcpCall
}

func newFakeMCP(t *testing.T, reply func(name string, args map[string]any) string) *fakeMCP {
	t.Helper()
	f := &fakeMCP{tools: testTools, manifest: testManifest, reply: reply}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	t.Setenv("EMPIRIO_MCP_URL", f.URL+"/mcp")
	t.Setenv("EMPIRIOLABS_API_KEY", testKey)
	t.Setenv("EMPIRIO_CONFIG_DIR", t.TempDir())
	return f
}

func (f *fakeMCP) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/manifest.json" && f.manifest != "" {
		w.Write([]byte(f.manifest))
		return
	}
	if r.URL.Path != "/mcp" || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	if f.redirect != "" {
		http.Redirect(w, r, f.redirect, http.StatusTemporaryRedirect)
		return
	}
	if f.status != 0 {
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="https://example.test/.well-known/oauth-protected-resource/mcp"`)
		w.WriteHeader(f.status)
		w.Write([]byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32001,"message":"A valid API key is required."}}`))
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+testKey || !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var msg struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		} `json:"params"`
	}
	json.NewDecoder(r.Body).Decode(&msg)
	f.mu.Lock()
	f.calls = append(f.calls, mcpCall{Method: msg.Method, Name: msg.Params.Name, Args: msg.Params.Arguments, Header: r.Header.Clone()})
	f.mu.Unlock()
	if msg.ID == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	var result string
	switch msg.Method {
	case "initialize":
		result = `{"protocolVersion": "2025-06-18", "capabilities": {"tools": {}}, "serverInfo": {"name": "test"}}`
	case "tools/list":
		result = `{"tools": ` + f.tools + `}`
	case "tools/call":
		if f.rpcError == "" {
			result = f.reply(msg.Params.Name, msg.Params.Arguments)
		}
	default:
		result = `{}`
	}
	body := `{"jsonrpc": "2.0", "id": ` + string(msg.ID) + `, "result": ` + result + `}`
	if msg.Method == "tools/call" && f.rpcError != "" {
		body = `{"jsonrpc": "2.0", "id": ` + string(msg.ID) + `, "error": ` + f.rpcError + `}`
	}
	if f.sse {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ": keep-alive\n\nevent: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{}}\n\n")
		// Every line of an event's data carries its own data: field.
		fmt.Fprintf(w, "event: message\r\ndata: %s\r\n\r\n", strings.ReplaceAll(body, "\n", "\r\ndata: "))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(body))
}

func (f *fakeMCP) toolCalls() []mcpCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []mcpCall
	for _, c := range f.calls {
		if c.Method == "tools/call" {
			out = append(out, c)
		}
	}
	return out
}

// capture sends the command's output to buffers and checks, at the end, that
// neither one shows the key.
func capture(t *testing.T) (out, errs *bytes.Buffer) {
	t.Helper()
	t.Setenv("NO_COLOR", "1")
	out, errs = &bytes.Buffer{}, &bytes.Buffer{}
	oldOut, oldErr := stdout, stderr
	stdout, stderr = out, errs
	t.Cleanup(func() {
		stdout, stderr = oldOut, oldErr
		if strings.Contains(out.String()+errs.String(), testKey) {
			t.Error("the key was printed")
		}
	})
	return out, errs
}

// mediaServer serves output files over https; downloads trust its certificate.
func mediaServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	old := downloadClient.Transport
	downloadClient.Transport = srv.Client().Transport
	t.Cleanup(func() { downloadClient.Transport = old })
	return srv
}

func textResult(text string) string {
	b, _ := json.Marshal(map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": false})
	return string(b)
}

func TestToolsListsEveryToolByToolset(t *testing.T) {
	f := newFakeMCP(t, nil)
	out, _ := capture(t)
	if code, err := toolsCmd(nil); code != 0 || err != nil {
		t.Fatalf("tools: %d %v", code, err)
	}
	got := out.String()
	for _, want := range []string{"EmpirioLabs AI tools you can run (5)", "Models (models): Browse the catalog", "Media (media)", "generate_image  Generate images from a prompt.", "Other", "mystery_tool"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Index(got, "list_models") > strings.Index(got, "generate_image") || strings.Index(got, "get_job") > strings.Index(got, "mystery_tool") {
		t.Errorf("not in the manifest's order:\n%s", got)
	}
	if strings.Contains(got, "Search (search)") {
		t.Errorf("a toolset with no tools for the account is listed:\n%s", got)
	}
	// The MCP handshake comes first, and every later request names the agreed version.
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) < 3 || f.calls[0].Method != "initialize" || f.calls[1].Method != "notifications/initialized" || f.calls[2].Method != "tools/list" {
		t.Fatalf("calls: %+v", f.calls)
	}
	if v := f.calls[2].Header.Get("MCP-Protocol-Version"); v != "2025-06-18" {
		t.Errorf("tools/list sent protocol version %q", v)
	}
}

func TestToolsReadsAnEventStream(t *testing.T) {
	f := newFakeMCP(t, nil)
	f.sse = true
	out, _ := capture(t)
	if code, err := toolsCmd([]string{"media"}); code != 0 || err != nil {
		t.Fatalf("tools media: %d %v", code, err)
	}
	if got := out.String(); !strings.Contains(got, "Media tools (1)") || !strings.Contains(got, "generate_image") || strings.Contains(got, "list_models") {
		t.Fatalf("tools media:\n%s", got)
	}
}

func TestToolsWithoutTheManifestIsOneList(t *testing.T) {
	f := newFakeMCP(t, nil)
	f.manifest = ""
	out, _ := capture(t)
	if code, err := toolsCmd(nil); code != 0 || err != nil {
		t.Fatalf("tools: %d %v", code, err)
	}
	if got := out.String(); !strings.Contains(got, "(5)") || strings.Contains(got, "(media)") || !strings.Contains(got, "mystery_tool") {
		t.Fatalf("tools:\n%s", got)
	}
}

func TestToolsShowsOneToolsArguments(t *testing.T) {
	newFakeMCP(t, nil)
	out, _ := capture(t)
	if code, err := toolsCmd([]string{"generate_image"}); code != 0 || err != nil {
		t.Fatalf("tools generate_image: %d %v", code, err)
	}
	got := out.String()
	for _, want := range []string{"generate_image: Generate an image", "prompt  string, required", "num_images  integer", "From 1 to 4. Default 1.", "image  array of strings", "Run it: empirio run generate_image prompt=<prompt> [name=value ...]"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Index(got, "  model ") > strings.Index(got, "  prompt ") {
		t.Errorf("arguments are not in the schema's order:\n%s", got)
	}
}

// "search" names a toolset and a tool: the toolset is listed, and the tool is named.
func TestToolsetAndToolOfTheSameName(t *testing.T) {
	f := newFakeMCP(t, nil)
	f.manifest = strings.Replace(testManifest, `"tools": []`, `"tools": ["mystery_tool"]`, 1)
	out, _ := capture(t)
	if code, err := toolsCmd([]string{"search"}); code != 0 || err != nil {
		t.Fatalf("tools search: %d %v", code, err)
	}
	if got := out.String(); !strings.Contains(got, "Search tools (1)") || !strings.Contains(got, "`empirio run search --help`") {
		t.Fatalf("tools search:\n%s", got)
	}
	out.Reset()
	if code, err := runCmd([]string{"search", "--help"}); code != 0 || err != nil || !strings.Contains(out.String(), "query  string, required") {
		t.Fatalf("run search --help: %d %v\n%s", code, err, out.String())
	}
}

func TestToolsRefusesAnUnknownName(t *testing.T) {
	newFakeMCP(t, nil)
	capture(t)
	code, err := toolsCmd([]string{"image"})
	if code != 2 || err == nil || !strings.Contains(err.Error(), "did you mean generate_image") {
		t.Fatalf("tools image: %d %v", code, err)
	}
}

func TestToolsJSON(t *testing.T) {
	newFakeMCP(t, nil)
	out, _ := capture(t)
	if code, err := toolsCmd([]string{"--json"}); code != 0 || err != nil {
		t.Fatalf("tools --json: %d %v", code, err)
	}
	var got struct {
		Tools []map[string]any `json:"tools"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || len(got.Tools) != 5 {
		t.Fatalf("tools --json: %v\n%s", err, out.String())
	}
	if got.Tools[0]["name"] != "list_models" || got.Tools[0]["toolset"] != "models" || got.Tools[0]["inputSchema"] == nil {
		t.Errorf("first tool: %v", got.Tools[0])
	}
}

func TestTypeValue(t *testing.T) {
	s, err := parseSchema(json.RawMessage(`{"type": "object", "properties": {
		"n": {"type": "integer"}, "x": {"type": "number"}, "b": {"type": "boolean"}, "s": {"type": "string"},
		"list": {"type": "array", "items": {"type": "string"}}, "nums": {"type": "array", "items": {"type": "integer"}},
		"obj": {"type": "object"}, "either": {"anyOf": [{"type": "string"}, {"type": "array", "items": {"type": "string"}}]},
		"choice": {"anyOf": [{"type": "string", "enum": ["auto", "none"]}, {"type": "object"}]},
		"mode": {"type": "string", "enum": ["fast", "slow"]}, "anything": {"description": "untyped"},
		"multi": {"type": ["integer", "string"]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	ok := []struct {
		name, text string
		want       any
	}{
		{"n", "4", json.Number("4")},
		{"n", " -12 ", json.Number("-12")},
		{"x", "0.7", json.Number("0.7")},
		{"x", "1e3", json.Number("1e3")},
		{"b", "true", true},
		{"b", "No", false},
		{"s", "007", "007"},
		{"s", "  keeps spaces ", "  keeps spaces "},
		{"s", `{"not": "parsed"}`, `{"not": "parsed"}`},
		{"list", `["a", "b"]`, []any{"a", "b"}},
		{"list", "https://example.test/one.png", []any{"https://example.test/one.png"}},
		{"nums", "[1, 2]", []any{json.Number("1"), json.Number("2")}},
		{"obj", `{"seed": 7}`, map[string]any{"seed": json.Number("7")}},
		{"either", "https://example.test/a.png", "https://example.test/a.png"},
		{"either", `["a"]`, []any{"a"}},
		{"choice", "auto", "auto"},
		{"choice", `{"type": "function"}`, map[string]any{"type": "function"}},
		{"mode", "fast", "fast"},
		{"anything", "12", json.Number("12")},
		{"anything", "plain words", "plain words"},
		{"multi", "5", json.Number("5")},
		{"multi", "five", "five"},
	}
	for _, c := range ok {
		got, err := typeValue(c.name, c.text, s.props[c.name])
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s=%s: got %#v %v, want %#v", c.name, c.text, got, err, c.want)
		}
	}
	bad := []struct{ name, text, msg string }{
		{"n", "two", "n takes an integer"},
		{"n", "1.5", "n takes an integer"},
		{"x", "NaN", "x takes a number"},
		{"b", "maybe", "b takes a boolean"},
		{"obj", `{"broken": `, "not valid JSON"},
		{"obj", "[1]", "obj takes an object"},
		{"nums", "3", "nums takes an array of integers"},
		{"mode", "medium", "mode must be one of: fast, slow"},
	}
	for _, c := range bad {
		if _, err := typeValue(c.name, c.text, s.props[c.name]); err == nil || !strings.Contains(err.Error(), c.msg) {
			t.Errorf("%s=%s: want %q, got %v", c.name, c.text, c.msg, err)
		}
	}
}

func testTool(t *testing.T, name string) (mcpTool, *schema) {
	t.Helper()
	var tools []mcpTool
	if err := json.Unmarshal([]byte(testTools), &tools); err != nil {
		t.Fatal(err)
	}
	tool, ok := findTool(tools, name)
	if !ok {
		t.Fatalf("no tool %s", name)
	}
	s, err := parseSchema(tool.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	return tool, s
}

func TestBuildArgumentsChecksTheSchemaFirst(t *testing.T) {
	tool, s := testTool(t, "generate_image")
	flags := runFlags{wait: defaultWait}
	args, err := buildArguments(tool, s, []string{"prompt=a red fox", "num_images=2", `parameters={"seed": 7}`}, flags)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"prompt": "a red fox", "num_images": json.Number("2"), "parameters": map[string]any{"seed": json.Number("7")}, "wait_seconds": 50}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args %#v", args)
	}
	for _, c := range []struct {
		pairs []string
		msg   string
	}{
		{[]string{"promt=a fox"}, `generate_image has no argument "promt"; it takes model, prompt (required), num_images, image, parameters, wait_seconds`},
		{[]string{"model=x"}, "generate_image needs prompt; it takes model, prompt (required)"},
		{[]string{"a fox"}, `arguments are name=value, not "a fox"`},
		{[]string{"prompt=x", "num_images=many"}, "num_images takes an integer"},
	} {
		if _, err := buildArguments(tool, s, c.pairs, flags); err == nil || !strings.Contains(err.Error(), c.msg) {
			t.Errorf("%v: want %q, got %v", c.pairs, c.msg, err)
		}
	}

	// --stdin, then --json, then name=value: each later one wins.
	old := stdin
	stdin = strings.NewReader(`{"prompt": "from stdin", "model": "m1", "num_images": 3}`)
	defer func() { stdin = old }()
	flags = runFlags{wait: 20, stdin: true, argsJSON: `{"model": "m2", "num_images": 4}`}
	args, err = buildArguments(tool, s, []string{"num_images=1"}, flags)
	if err != nil {
		t.Fatal(err)
	}
	if args["prompt"] != "from stdin" || args["model"] != "m2" || args["num_images"] != json.Number("1") || args["wait_seconds"] != 20 {
		t.Fatalf("merged %#v", args)
	}
	// An argument in --json is checked like a pair, and a wait the user set stays.
	if _, err := buildArguments(tool, s, nil, runFlags{argsJSON: `{"prompt": "x", "colour": "red"}`}); err == nil || !strings.Contains(err.Error(), `no argument "colour"`) {
		t.Errorf("unknown name in --json: %v", err)
	}
	if _, err := buildArguments(tool, s, nil, runFlags{argsJSON: `["x"]`}); err == nil || !strings.Contains(err.Error(), "needs a JSON object") {
		t.Errorf("a JSON list: %v", err)
	}
	args, _ = buildArguments(tool, s, []string{"prompt=x", "wait_seconds=5"}, runFlags{wait: 600})
	if args["wait_seconds"] != json.Number("5") {
		t.Errorf("wait_seconds %#v", args["wait_seconds"])
	}
	// A schema that allows other names passes them on.
	mystery, ms := testTool(t, "mystery_tool")
	if args, err := buildArguments(mystery, ms, []string{"anything=1"}, runFlags{}); err != nil || args["anything"] != json.Number("1") {
		t.Errorf("open schema: %v %v", args, err)
	}
}

func TestParseRun(t *testing.T) {
	tool, pairs, f, err := parseRun([]string{"generate_image", "prompt=a b", "--out", "imgs", "--wait=30", "--no-download", "n=1", "--json-output"})
	if err != nil || tool != "generate_image" || strings.Join(pairs, "|") != "prompt=a b|n=1" || f.out != "imgs" || f.wait != 30 || !f.noDownload || !f.jsonOutput {
		t.Fatalf("%q %q %+v %v", tool, pairs, f, err)
	}
	if _, _, f, _ := parseRun([]string{"x"}); f.wait != defaultWait || f.out != "." {
		t.Fatalf("defaults %+v", f)
	}
	if _, pairs, _, err := parseRun([]string{"chat", "--", "prompt=--json"}); err != nil || pairs[0] != "prompt=--json" {
		t.Fatalf("after --: %v %v", pairs, err)
	}
	for _, bad := range [][]string{{"x", "--wait", "soon"}, {"x", "--wait=-1"}, {"x", "--out"}, {"x", "--json"}, {"x", "--nope"}} {
		if _, _, _, err := parseRun(bad); err == nil {
			t.Errorf("%v was accepted", bad)
		}
	}
}

func TestRunPrintsTheTextAndSavesMediaSafely(t *testing.T) {
	media := mediaServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("a download carried credentials")
		}
		switch r.URL.EscapedPath() {
		case "/worker-outputs/abc123.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write([]byte("png bytes"))
		case "/download/..%5C..%5Cevil":
			w.Header().Set("Content-Type", "image/webp")
			w.Write([]byte("webp bytes"))
		case "/page":
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<html>"))
		default:
			http.NotFound(w, r)
		}
	})
	png := media.URL + "/worker-outputs/abc123.png?token=t0k&expires=99"
	evil := media.URL + "/download/..%5C..%5Cevil"
	f := newFakeMCP(t, func(name string, args map[string]any) string {
		b, _ := json.Marshal(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "The output is ready.\n" + png},
				{"type": "resource_link", "uri": png, "name": "Generate an image 1"},
				{"type": "resource_link", "uri": evil, "name": "Generate an image 2"},
				{"type": "resource_link", "uri": media.URL + "/page", "name": "not media"},
				{"type": "resource_link", "uri": "http://example.test/clear.png", "name": "not https"},
			},
			"structuredContent": map[string]any{"data": []any{map[string]any{"url": png}}},
			"isError":           false,
		})
		return string(b)
	})
	dir := t.TempDir()
	out, errs := capture(t)
	for i := 0; i < 2; i++ {
		code, err := runCmd([]string{"generate_image", "prompt=a lighthouse", "--out", dir})
		if code != 0 || err != nil {
			t.Fatalf("run: %d %v\n%s", code, err, errs.String())
		}
	}
	if !strings.Contains(out.String(), "The output is ready.") {
		t.Errorf("stdout: %s", out.String())
	}
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	want := []string{"_.._evil-1.webp", "_.._evil.webp", "abc123-1.png", "abc123.png"}
	if strings.Join(names, " ") != strings.Join(want, " ") {
		// The second run never overwrites the first; the escaping name stays in dir.
		t.Fatalf("saved %v, want %v", names, want)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "abc123.png")); string(b) != "png bytes" {
		t.Errorf("abc123.png holds %q", b)
	}
	if !strings.Contains(errs.String(), "Saved "+filepath.Join(dir, "abc123.png")) && !strings.Contains(errs.String(), "abc123.png (9 bytes)") {
		t.Errorf("stderr: %s", errs.String())
	}
	calls := f.toolCalls()
	if len(calls) != 2 || calls[0].Args["prompt"] != "a lighthouse" || calls[0].Args["wait_seconds"] != float64(50) {
		t.Fatalf("calls %+v", calls)
	}
}

func TestRunSavesInlineMediaAndHonoursNoDownload(t *testing.T) {
	audio := base64.StdEncoding.EncodeToString([]byte("mp3 bytes"))
	newFakeMCP(t, func(name string, args map[string]any) string {
		return `{"content": [{"type": "text", "text": "Returned inline."}, {"type": "audio", "data": "` + audio + `", "mimeType": "audio/mpeg"}], "isError": false}`
	})
	dir := t.TempDir()
	capture(t)
	if code, err := runCmd([]string{"generate_image", "prompt=x", "--out", dir, "--no-download"}); code != 0 || err != nil {
		t.Fatalf("--no-download: %d %v", code, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("--no-download saved %d files", len(entries))
	}
	if code, err := runCmd([]string{"generate_image", "prompt=x", "--out", dir}); code != 0 || err != nil {
		t.Fatalf("run: %d %v", code, err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "generate_image.mp3")); err != nil || string(b) != "mp3 bytes" {
		t.Fatalf("inline audio: %q %v", b, err)
	}
}

// Without resource links, only a tool that makes something has its result
// searched for media, and never for the request's own inputs or plain http.
func TestRunFindsMediaLinksOnlyInOutputs(t *testing.T) {
	media := mediaServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write([]byte("bytes of " + r.URL.Path))
	})
	input := media.URL + "/inputs/reference.png"
	reply := func(name string, args map[string]any) string {
		return textResult("Done: " + media.URL + "/out/clip.mp4, see also http://example.test/x.png and " + input + ".")
	}
	newFakeMCP(t, reply)
	dir := t.TempDir()
	capture(t)
	if code, err := runCmd([]string{"generate_image", "prompt=x", "image=" + input, "--out", dir}); code != 0 || err != nil {
		t.Fatalf("run: %d %v", code, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 || entries[0].Name() != "clip.mp4" {
		t.Fatalf("saved %v", entries)
	}
	// A read-only tool, such as a list or a search, saves nothing.
	other := t.TempDir()
	if code, err := runCmd([]string{"list_models", "--out", other}); code != 0 || err != nil {
		t.Fatalf("list_models: %d %v", code, err)
	}
	if entries, _ := os.ReadDir(other); len(entries) != 0 {
		t.Fatalf("a read-only tool saved %v", entries)
	}
}

func TestDownloadsStayOnHTTPSAndWithinTheLimit(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("clear")) }))
	defer plain.Close()
	media := mediaServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect.png":
			http.Redirect(w, r, plain.URL+"/x.png", http.StatusFound)
		default:
			w.Header().Set("Content-Type", "video/mp4")
			w.Write(bytes.Repeat([]byte("v"), 4096))
		}
	})
	newFakeMCP(t, func(name string, args map[string]any) string {
		return `{"content": [{"type": "resource_link", "uri": "` + media.URL + `/redirect.png"}, {"type": "resource_link", "uri": "` + media.URL + `/big.mp4"}], "isError": false}`
	})
	old := maxDownload
	maxDownload = 1024
	defer func() { maxDownload = old }()
	dir := t.TempDir()
	_, errs := capture(t)
	code, err := runCmd([]string{"generate_image", "prompt=x", "--out", dir})
	if code != 1 || err == nil || !strings.Contains(err.Error(), "2 of 2 outputs could not be saved") {
		t.Fatalf("run: %d %v", code, err)
	}
	if !strings.Contains(errs.String(), "redirected away from https") || !strings.Contains(errs.String(), "larger than 1 KB") {
		t.Errorf("stderr: %s", errs.String())
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("left %v", entries)
	}
}

func TestSafeFileName(t *testing.T) {
	cases := map[string]string{
		"abc123.png":           "abc123.png",
		"../../etc/passwd":     "_.._etc_passwd",
		`..\..\evil.png`:       "_.._evil.png",
		".hidden.png":          "hidden.png",
		"-rf.png":              "rf.png",
		"CON.png":              "file-CON.png",
		"nul":                  "file-nul",
		"name with spaces.mp4": "name_with_spaces.mp4",
		"ünïcode.wav":          "_n_code.wav",
		"trailing.":            "trailing",
		"/":                    "",
		"...":                  "",
		"a:b.glb":              "a_b.glb",
	}
	for in, want := range cases {
		if got := safeFileName(in); got != want {
			t.Errorf("safeFileName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := safeFileName(strings.Repeat("x", 300) + ".png"); len(got) != 104 {
		t.Errorf("long name kept %d characters", len(got))
	}
}

func TestRunFollowsAJobWithTheServersJobTool(t *testing.T) {
	media := mediaServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.Write([]byte("mp4"))
	})
	var polls atomic.Int32
	f := newFakeMCP(t, func(name string, args map[string]any) string {
		switch name {
		case "generate_image":
			return `{"content": [{"type": "text", "text": "Still generating (job j1). Call get_job."}], "structuredContent": {"job_id": "j1", "status": "processing"}, "isError": false}`
		case "get_job":
			if polls.Add(1) < 2 {
				return `{"content": [{"type": "text", "text": "Still generating (job j1)."}], "structuredContent": {"job_id": "j1", "status": "running"}, "isError": false}`
			}
			return `{"content": [{"type": "text", "text": "The output is ready."}, {"type": "resource_link", "uri": "` + media.URL + `/v/j1.mp4"}], "structuredContent": {"job_id": "j1", "status": "completed"}, "isError": false}`
		}
		return textResult("?")
	})
	oldInterval := jobPollInterval
	jobPollInterval = 10 * time.Millisecond
	defer func() { jobPollInterval = oldInterval }()
	dir := t.TempDir()
	out, errs := capture(t)
	if code, err := runCmd([]string{"generate_image", "prompt=x", "--out", dir}); code != 0 || err != nil {
		t.Fatalf("run: %d %v\n%s", code, err, errs.String())
	}
	if strings.Contains(out.String(), "Still generating") || !strings.Contains(out.String(), "The output is ready.") {
		t.Errorf("stdout: %s", out.String())
	}
	if !strings.Contains(errs.String(), "Job j1 is processing; waiting up to 10 minutes") {
		t.Errorf("stderr: %s", errs.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "j1.mp4")); err != nil {
		t.Fatal(err)
	}
	calls := f.toolCalls()
	if len(calls) != 3 || calls[1].Name != "get_job" || calls[1].Args["job_id"] != "j1" {
		t.Fatalf("calls %+v", calls)
	}
	if w, _ := calls[1].Args["wait_seconds"].(float64); w < 49 || w > 50 {
		t.Errorf("get_job waited %v seconds", calls[1].Args["wait_seconds"])
	}
}

func TestRunGivesUpOnAJobAtTheDeadline(t *testing.T) {
	newFakeMCP(t, func(name string, args map[string]any) string {
		return `{"content": [{"type": "text", "text": "Still generating."}], "structuredContent": {"job_id": "j2", "status": "processing"}, "isError": false}`
	})
	out, _ := capture(t)
	start := time.Now()
	code, err := runCmd([]string{"generate_image", "prompt=x", "--wait", "1"})
	if code != 1 || err == nil || !strings.Contains(err.Error(), "job j2 is still processing after 1 second; check on it later with `empirio run get_job job_id=j2`") {
		t.Fatalf("run: %d %v", code, err)
	}
	if time.Since(start) > 10*time.Second || out.Len() != 0 {
		t.Fatalf("took %v, printed %q", time.Since(start), out.String())
	}
}

func TestRunWithWaitZeroReturnsTheJob(t *testing.T) {
	f := newFakeMCP(t, func(name string, args map[string]any) string {
		return `{"content": [{"type": "text", "text": "Still generating (job j3)."}], "structuredContent": {"job_id": "j3", "status": "queued"}, "isError": false}`
	})
	out, errs := capture(t)
	if code, err := runCmd([]string{"generate_image", "prompt=x", "--wait", "0"}); code != 0 || err != nil {
		t.Fatalf("run: %d %v", code, err)
	}
	if !strings.Contains(out.String(), "job j3") || !strings.Contains(errs.String(), "`empirio run get_job job_id=j3`") {
		t.Fatalf("stdout %q stderr %q", out.String(), errs.String())
	}
	if calls := f.toolCalls(); len(calls) != 1 || calls[0].Args["wait_seconds"] != float64(0) {
		t.Fatalf("calls %+v", calls)
	}
}

// Without a job tool in the list, the job is read from the jobs API with the same key.
func TestRunFollowsAJobThroughTheAPI(t *testing.T) {
	media := mediaServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "model/gltf-binary")
		w.Write([]byte("glb"))
	})
	var reads atomic.Int32
	jobs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/jobs/j%2F4" && r.URL.EscapedPath() != "/v1/jobs/j%2F4" {
			t.Errorf("path %s", r.URL.EscapedPath())
		}
		if r.Header.Get("Authorization") != "Bearer "+testKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if reads.Add(1) < 3 {
			w.Write([]byte(`{"job_id": "j/4", "status": "processing"}`))
			return
		}
		w.Write([]byte(`{"job_id": "j/4", "status": "completed", "result": {"data": [{"url": "` + media.URL + `/m/model.glb"}], "input": {"image": "` + media.URL + `/in.png"}}}`))
	}))
	defer jobs.Close()
	f := newFakeMCP(t, func(name string, args map[string]any) string {
		return `{"content": [{"type": "text", "text": "Started."}], "structuredContent": {"job_id": "j/4", "status": "processing"}, "isError": false}`
	})
	var tools []map[string]any
	json.Unmarshal([]byte(testTools), &tools)
	var withoutJobs []map[string]any
	for _, tool := range tools {
		if tool["name"] != "get_job" {
			withoutJobs = append(withoutJobs, tool)
		}
	}
	b, _ := json.Marshal(withoutJobs)
	f.tools = string(b)
	t.Setenv("EMPIRIOLABS_BASE_URL", jobs.URL)
	oldInterval := jobPollInterval
	jobPollInterval = 10 * time.Millisecond
	defer func() { jobPollInterval = oldInterval }()
	dir := t.TempDir()
	out, errs := capture(t)
	if code, err := runCmd([]string{"generate_image", "prompt=x", "--out", dir}); code != 0 || err != nil {
		t.Fatalf("run: %d %v\n%s", code, err, errs.String())
	}
	if reads.Load() != 3 || !strings.Contains(out.String(), `"status": "completed"`) {
		t.Fatalf("reads %d, stdout %s", reads.Load(), out.String())
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 || entries[0].Name() != "model.glb" {
		t.Fatalf("saved %v", entries)
	}
}

func TestRunReportsAToolError(t *testing.T) {
	newFakeMCP(t, func(name string, args map[string]any) string {
		return `{"content": [{"type": "text", "text": "Job 'nope' not found"}], "isError": true}`
	})
	out, _ := capture(t)
	code, err := runCmd([]string{"get_job", "job_id=nope"})
	if code != 1 || err == nil || err.Error() != "Job 'nope' not found" || out.Len() != 0 {
		t.Fatalf("run: %d %v %q", code, err, out.String())
	}
	code, err = runCmd([]string{"get_job", "job_id=nope", "--json-output"})
	var raw map[string]any
	if code != 1 || err == nil || json.Unmarshal(out.Bytes(), &raw) != nil || raw["isError"] != true {
		t.Fatalf("--json-output: %d %v %q", code, err, out.String())
	}
}

func TestRunJSONOutputIsTheResult(t *testing.T) {
	newFakeMCP(t, func(name string, args map[string]any) string {
		return `{"content": [{"type": "text", "text": "2 models."}], "structuredContent": {"total": 2, "data": [{"id": "z"}, {"id": "a"}]}, "isError": false}`
	})
	out, _ := capture(t)
	if code, err := runCmd([]string{"list_models", "limit=2", "--json-output"}); code != 0 || err != nil {
		t.Fatalf("run: %d %v", code, err)
	}
	var got toolResult
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.text() != "2 models." || !strings.Contains(string(got.Structured), `"id": "z"`) {
		t.Fatalf("stdout %s %v", out.String(), err)
	}
}

func TestARefusedKeySaysHowToSignIn(t *testing.T) {
	f := newFakeMCP(t, nil)
	f.status = http.StatusUnauthorized
	capture(t)
	if code, err := runCmd([]string{"list_models"}); code != 1 || err == nil || !strings.Contains(err.Error(), "EMPIRIOLABS_API_KEY was not accepted") {
		t.Fatalf("env key: %d %v", code, err)
	}
	t.Setenv("EMPIRIOLABS_API_KEY", "")
	if err := config.Save(config.Config{APIKey: testKey, KeySource: "browser"}); err != nil {
		t.Fatal(err)
	}
	if code, err := toolsCmd(nil); code != 1 || err == nil || !strings.Contains(err.Error(), "run `empirio login`") {
		t.Fatalf("saved key: %d %v", code, err)
	}
}

func TestRunRefusesAnUnknownTool(t *testing.T) {
	newFakeMCP(t, func(string, map[string]any) string { return textResult("ok") })
	capture(t)
	if code, err := runCmd([]string{"generate-image", "prompt=dash for underscore", "--no-download"}); code != 0 || err != nil {
		t.Fatalf("generate-image: %d %v", code, err)
	}
	if code, err := runCmd([]string{"make_coffee"}); code != 2 || err == nil || !strings.Contains(err.Error(), `unknown tool "make_coffee"`) {
		t.Fatalf("make_coffee: %d %v", code, err)
	}
	if code, err := runCmd(nil); code != 2 || err == nil || !strings.Contains(err.Error(), "usage: empirio run <tool>") {
		t.Fatalf("no tool: %d %v", code, err)
	}
}

func TestReadEventStreamFindsTheReply(t *testing.T) {
	stream := ": comment\n\nevent: message\nid: 1\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/message\",\"params\":{}}\n\n" +
		"data: {\"jsonrpc\":\"2.0\",\ndata:  \"id\":7,\"result\":{\"ok\":true}}\n\n"
	reply, err := readEventStream(strings.NewReader(stream), 7)
	if err != nil || string(reply.Result) != `{"ok":true}` {
		t.Fatalf("%s %v", reply.Result, err)
	}
	if _, err := readEventStream(strings.NewReader("data: {\"jsonrpc\":\"2.0\",\"id\":8,\"result\":{}}\n\n"), 7); err == nil {
		t.Fatal("a reply to another request was taken")
	}
	reply, err = readEventStream(strings.NewReader("data: {\"jsonrpc\":\"2.0\",\"id\":3,\"error\":{\"code\":-32602,\"message\":\"Unknown tool\"}}"), 3)
	if err != nil || reply.Error == nil || reply.Error.Message != "Unknown tool" {
		t.Fatalf("error reply: %+v %v", reply, err)
	}
}

func TestPrintableDropsTerminalControl(t *testing.T) {
	in := "ok\x1b]52;c;ZXZpbA==\x07 and \x1b[2Jcleared\r\nnext\tcol\u009b"
	if got := printable(in); got != "ok]52;c;ZXZpbA== and [2Jcleared\nnext\tcol" {
		t.Fatalf("%q", got)
	}
}

// The key goes to the MCP server's own address only: a redirect is refused,
// never followed.
func TestTheKeyIsNeverRedirected(t *testing.T) {
	var reached atomic.Int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Add(1) }))
	defer elsewhere.Close()
	f := newFakeMCP(t, nil)
	f.redirect = elsewhere.URL + "/mcp"
	capture(t)
	if code, err := toolsCmd(nil); code != 1 || err == nil || !strings.Contains(err.Error(), "HTTP 307") {
		t.Fatalf("tools: %d %v", code, err)
	}
	if reached.Load() != 0 {
		t.Fatal("the request followed the redirect")
	}
}

func TestServerErrorsKeepTheirMessage(t *testing.T) {
	f := newFakeMCP(t, nil)
	f.rpcError = `{"code": -32602, "message": "Unknown tool 'list_models'. Call tools/list for the available tools."}`
	capture(t)
	if code, err := runCmd([]string{"list_models"}); code != 1 || err == nil || !strings.Contains(err.Error(), "Unknown tool 'list_models'") {
		t.Fatalf("JSON-RPC error: %d %v", code, err)
	}
	f.status = http.StatusForbidden
	if code, err := toolsCmd(nil); code != 1 || err == nil || err.Error() != "the MCP server refused the request: A valid API key is required. (HTTP 403)" {
		t.Fatalf("403: %d %v", code, err)
	}
}

// A job read that comes back at once, still running, is not repeated in a
// tight loop: reads stay at least the poll interval apart until the deadline.
func TestRunPacesJobReads(t *testing.T) {
	var reads atomic.Int32
	newFakeMCP(t, func(name string, args map[string]any) string {
		if name == "get_job" {
			reads.Add(1)
		}
		return `{"content": [{"type": "text", "text": "Still generating."}], "structuredContent": {"job_id": "j5", "status": "processing"}, "isError": false}`
	})
	oldInterval := jobPollInterval
	jobPollInterval = 100 * time.Millisecond
	defer func() { jobPollInterval = oldInterval }()
	capture(t)
	if code, err := runCmd([]string{"generate_image", "prompt=x", "--wait", "2"}); code != 1 || err == nil {
		t.Fatalf("run: %d %v", code, err)
	}
	if n := reads.Load(); n < 3 || n > 20 {
		t.Fatalf("%d job reads in about a second", n)
	}
}
