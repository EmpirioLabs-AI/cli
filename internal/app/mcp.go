package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/EmpirioLabs-AI/cli/internal/agents"
	"github.com/EmpirioLabs-AI/cli/internal/config"
)

// The EmpirioLabs AI MCP server is the one source of the tools `empirio tools`
// lists and `empirio run` runs: the server builds them from the API's OpenAPI
// document, so a new API operation becomes a command here without a release.
// The CLI speaks MCP's Streamable HTTP transport to it: one JSON-RPC 2.0
// message per POST, with the reply as JSON or as server-sent events.
const (
	// mcpProtocolVersion is the MCP revision the CLI asks for. It is the first
	// with resource links, through which the server names a tool's output files.
	mcpProtocolVersion = "2025-06-18"
	// maxMCPReply bounds one reply: a tool returns at most a few MB of text,
	// structured content and inline media.
	maxMCPReply = 32 << 20
	// listTimeout bounds the handshake and the tool list; callTimeout one tool
	// call, which the server ends well before it (its longest tool runs about
	// six minutes, a job wait included).
	listTimeout = 30 * time.Second
	callTimeout = 10 * time.Minute
)

// mcpBase is the MCP server's endpoint: EMPIRIO_MCP_URL (https, or http on this
// computer; see checkOverrides), else the EmpirioLabs AI server.
func mcpBase() string {
	base := strings.TrimSpace(os.Getenv("EMPIRIO_MCP_URL"))
	if base == "" {
		base = agents.MCPBase
	}
	return strings.TrimRight(base, "/")
}

// mcpSession is one command's conversation with the MCP server, authenticated
// with the signed-in key as a Bearer token.
type mcpSession struct {
	url       string
	key       string
	keySource string // where the key came from, for the message when it is refused
	client    *http.Client
	version   string // the protocol version the server agreed to
	session   string // Mcp-Session-Id, for a server that keeps sessions
	lastID    int
}

func newMCPSession(url, key, keySource string) *mcpSession {
	return &mcpSession{
		url: url, key: key, keySource: keySource,
		// The key goes only where it was sent: a redirect is an error, never followed.
		client: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
}

// rpcError is a JSON-RPC error the server answered with.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("the MCP server answered with error %d", e.Code)
	}
	return e.Message
}

type rpcReply struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

// keyRefused is what a refused key means for the user.
func keyRefused(source string) error {
	if source == config.KeyEnv {
		return fmt.Errorf("the API key in %s was not accepted; set a valid key, or unset it and run `empirio login`", config.KeyEnv)
	}
	return errors.New("the API key was not accepted; run `empirio login` to sign in again")
}

// request sends one JSON-RPC request and returns its result.
func (s *mcpSession) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	s.lastID++
	id := s.lastID
	resp, err := s.post(ctx, map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	reply, err := readReply(resp, id)
	if err != nil {
		return nil, err
	}
	if reply.Error != nil {
		return nil, reply.Error
	}
	if len(reply.Result) == 0 {
		return nil, errors.New("the MCP server sent an empty reply")
	}
	return reply.Result, nil
}

// notify sends a JSON-RPC notification, which has no reply.
func (s *mcpSession) notify(ctx context.Context, method string) error {
	resp, err := s.post(ctx, map[string]any{"jsonrpc": "2.0", "method": method})
	if err != nil {
		return err
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	return nil
}

func (s *mcpSession) post(ctx context.Context, message any) (*http.Response, error) {
	body, err := json.Marshal(message)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("User-Agent", "empirio-cli/"+Version)
	req.Header.Set("Authorization", "Bearer "+s.key)
	if s.version != "" {
		req.Header.Set("MCP-Protocol-Version", s.version)
	}
	if s.session != "" {
		req.Header.Set("Mcp-Session-Id", s.session)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, errors.New("the EmpirioLabs AI MCP server did not answer in time")
		}
		return nil, fmt.Errorf("could not reach the EmpirioLabs AI MCP server: %w", err)
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		resp.Body.Close()
		return nil, keyRefused(s.keySource)
	case resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusAccepted:
		if id := resp.Header.Get("Mcp-Session-Id"); id != "" && s.session == "" {
			s.session = id
		}
		return resp, nil
	}
	defer resp.Body.Close()
	var failed rpcReply
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&failed) == nil && failed.Error != nil && failed.Error.Message != "" {
		return nil, fmt.Errorf("the MCP server refused the request: %s (HTTP %d)", failed.Error.Message, resp.StatusCode)
	}
	return nil, fmt.Errorf("the MCP server answered HTTP %d", resp.StatusCode)
}

// readReply finds the reply to request id in a JSON body or an event stream,
// passing over any notification the server sends first.
func readReply(resp *http.Response, id int) (rpcReply, error) {
	body := io.LimitReader(resp.Body, maxMCPReply+1)
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if mediaType == "text/event-stream" {
		return readEventStream(body, id)
	}
	raw, err := io.ReadAll(body)
	if err != nil {
		return rpcReply{}, fmt.Errorf("could not read the MCP server's reply: %w", err)
	}
	if len(raw) > maxMCPReply {
		return rpcReply{}, errors.New("the MCP server's reply is too large")
	}
	if reply, ok := matchReply(raw, id); ok {
		return reply, nil
	}
	return rpcReply{}, errors.New("the MCP server's reply could not be read")
}

// readEventStream reads server-sent events: each event's data lines, joined,
// carry one JSON-RPC message.
func readEventStream(body io.Reader, id int) (rpcReply, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64<<10), maxMCPReply)
	var data []string
	flush := func() (rpcReply, bool) {
		if len(data) == 0 {
			return rpcReply{}, false
		}
		reply, ok := matchReply([]byte(strings.Join(data, "\n")), id)
		data = data[:0]
		return reply, ok
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		switch {
		case line == "":
			if reply, ok := flush(); ok {
				return reply, nil
			}
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return rpcReply{}, errors.New("the MCP server's reply is too large")
		}
		return rpcReply{}, fmt.Errorf("could not read the MCP server's reply: %w", err)
	}
	if reply, ok := flush(); ok {
		return reply, nil
	}
	return rpcReply{}, errors.New("the MCP server ended its reply without an answer")
}

// matchReply returns the message answering id from one JSON-RPC message or a batch.
func matchReply(raw []byte, id int) (rpcReply, bool) {
	raw = bytes.TrimSpace(raw)
	var messages []rpcReply
	if len(raw) > 0 && raw[0] == '[' {
		if json.Unmarshal(raw, &messages) != nil {
			return rpcReply{}, false
		}
	} else {
		var one rpcReply
		if json.Unmarshal(raw, &one) != nil {
			return rpcReply{}, false
		}
		messages = []rpcReply{one}
	}
	want := strconv.Itoa(id)
	for _, m := range messages {
		got := strings.Trim(string(bytes.TrimSpace(m.ID)), `"`)
		if got == want && (m.Result != nil || m.Error != nil) {
			return m, true
		}
	}
	return rpcReply{}, false
}

// initialize is MCP's handshake: it agrees on the protocol version, which every
// later request names.
func (s *mcpSession) initialize(ctx context.Context) error {
	raw, err := s.request(ctx, "initialize", map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "empirio", "title": "EmpirioLabs AI CLI", "version": Version},
	})
	if err != nil {
		return err
	}
	var result struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return errors.New("the MCP server's handshake could not be read")
	}
	s.version = firstNonEmpty(result.ProtocolVersion, mcpProtocolVersion)
	return s.notify(ctx, "notifications/initialized")
}

// mcpTool is one tool as tools/list describes it.
type mcpTool struct {
	Name        string          `json:"name"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Annotations struct {
		Title    string `json:"title"`
		ReadOnly bool   `json:"readOnlyHint"`
	} `json:"annotations"`

	raw json.RawMessage // the definition as the server sent it
}

func (t mcpTool) title() string { return firstNonEmpty(t.Title, t.Annotations.Title) }

// listTools reads every tool the key may run, following the list's pages.
func (s *mcpSession) listTools(ctx context.Context) ([]mcpTool, error) {
	var tools []mcpTool
	cursor := ""
	for page := 0; page < 50; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := s.request(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		var result struct {
			Tools      []json.RawMessage `json:"tools"`
			NextCursor string            `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, errors.New("the MCP server's tool list could not be read")
		}
		for _, def := range result.Tools {
			var t mcpTool
			if json.Unmarshal(def, &t) != nil || t.Name == "" {
				continue
			}
			t.raw = def
			tools = append(tools, t)
		}
		if result.NextCursor == "" || result.NextCursor == cursor {
			break
		}
		cursor = result.NextCursor
	}
	return tools, nil
}

// contentBlock is one item of a tool result's content.
type contentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`     // image or audio, base64
	MimeType string `json:"mimeType,omitempty"` // of data, or of a resource link's target
	URI      string `json:"uri,omitempty"`      // resource_link
	Name     string `json:"name,omitempty"`
}

// toolResult is what tools/call returns.
type toolResult struct {
	Content    []contentBlock  `json:"content"`
	Structured json.RawMessage `json:"structuredContent,omitempty"`
	IsError    bool            `json:"isError"`
}

// text joins the result's text content.
func (r toolResult) text() string {
	var parts []string
	for _, c := range r.Content {
		if c.Type == "text" && strings.TrimSpace(c.Text) != "" {
			parts = append(parts, strings.TrimRight(c.Text, "\n"))
		}
	}
	return strings.Join(parts, "\n")
}

// callTool runs one tool and returns its result, and the result as the server sent it.
func (s *mcpSession) callTool(ctx context.Context, name string, arguments map[string]any) (toolResult, json.RawMessage, error) {
	raw, err := s.request(ctx, "tools/call", map[string]any{"name": name, "arguments": arguments})
	if err != nil {
		return toolResult{}, nil, err
	}
	var result toolResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return toolResult{}, nil, fmt.Errorf("the result of %s could not be read", name)
	}
	return result, raw, nil
}

// connect signs in when no key is set and the terminal can ask, then opens a
// session with the MCP server.
func connect() (*mcpSession, error) {
	key, source, err := config.Key()
	if errors.Is(err, config.ErrNoKey) && isTerminal(os.Stdin) {
		fmt.Fprintln(stderr, "Sign in to EmpirioLabs AI to continue.")
		if err := login(nil); err != nil {
			return nil, err
		}
		key, source, err = config.Key()
	}
	if err != nil {
		return nil, err
	}
	endpoint, err := agents.ToolsURL(mcpBase(), "all", nil) // every toolset
	if err != nil {
		return nil, err
	}
	s := newMCPSession(endpoint, key, source)
	ctx, cancel := context.WithTimeout(context.Background(), listTimeout)
	defer cancel()
	if err := s.initialize(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// tools reads the tool list the session's key may run.
func (s *mcpSession) tools() ([]mcpTool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), listTimeout)
	defer cancel()
	return s.listTools(ctx)
}
