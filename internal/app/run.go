package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/EmpirioLabs-AI/cli/internal/api"
)

// `empirio run` runs one EmpirioLabs AI tool: the arguments are typed and
// checked by the tool's input schema from the MCP server's live tool list, the
// result is printed, a job that is still running is followed, and the media
// the result names is saved.

const (
	runUsage = "usage: empirio run <tool> [name=value ...] [--json <object>] [--stdin] [--out <dir>] [--no-download] [--wait <seconds>] [--json-output]"
	// defaultWait is how long `empirio run` follows a job that is still running.
	defaultWait = 600
)

// stdin is where --stdin reads the arguments from.
var stdin io.Reader = os.Stdin

// jobPollInterval paces the job status reads when the server cannot wait for a job itself.
var jobPollInterval = 2 * time.Second

// finished are the job statuses that end a job.
var finished = map[string]bool{"completed": true, "succeeded": true, "failed": true, "cancelled": true, "canceled": true, "error": true}

// runFlags are `empirio run`'s own flags.
type runFlags struct {
	argsJSON   string
	stdin      bool
	out        string
	noDownload bool
	wait       int
	jsonOutput bool
	help       bool
}

// parseRun separates the tool's name, its name=value arguments and run's flags.
func parseRun(args []string) (tool string, pairs []string, f runFlags, err error) {
	f.wait, f.out = defaultWait, "."
	value := func(i int, flag, what string) (string, error) {
		if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
			return "", fmt.Errorf("%s needs %s", flag, what)
		}
		return args[i+1], nil
	}
	setWait := func(v string) error {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < 0 {
			return fmt.Errorf("--wait takes a number of seconds, such as 600 (0 does not wait), not %q", v)
		}
		f.wait = n
		return nil
	}
	pairsOnly := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if pairsOnly || !strings.HasPrefix(a, "-") {
			if tool == "" && !strings.Contains(a, "=") {
				tool = a
			} else {
				pairs = append(pairs, a)
			}
			continue
		}
		var v string
		switch {
		case a == "--":
			pairsOnly = true
		case a == "-h" || a == "--help":
			f.help = true
		case a == "--stdin":
			f.stdin = true
		case a == "--no-download":
			f.noDownload = true
		case a == "--json-output":
			f.jsonOutput = true
		case a == "--json":
			if v, err = value(i, a, "a JSON object of arguments"); err != nil {
				return
			}
			f.argsJSON, i = v, i+1
		case strings.HasPrefix(a, "--json="):
			f.argsJSON = strings.TrimPrefix(a, "--json=")
		case a == "--out":
			if v, err = value(i, a, "a folder"); err != nil {
				return
			}
			f.out, i = v, i+1
		case strings.HasPrefix(a, "--out="):
			f.out = strings.TrimPrefix(a, "--out=")
		case a == "--wait":
			if v, err = value(i, a, "a number of seconds"); err != nil {
				return
			}
			if err = setWait(v); err != nil {
				return
			}
			i++
		case strings.HasPrefix(a, "--wait="):
			if err = setWait(strings.TrimPrefix(a, "--wait=")); err != nil {
				return
			}
		default:
			err = fmt.Errorf("unknown flag %q (%s)", a, runUsage)
			return
		}
	}
	if strings.TrimSpace(f.out) == "" {
		err = errors.New("--out needs a folder")
	}
	return
}

// buildArguments merges the arguments --stdin, --json and name=value give, in
// that order, types the pairs by the tool's input schema and checks the
// result against it before anything is sent.
func buildArguments(t mcpTool, s *schema, pairs []string, f runFlags) (map[string]any, error) {
	args := map[string]any{}
	merge := func(what string, text string) error {
		v, err := decodeJSON(strings.TrimSpace(text))
		obj, ok := v.(map[string]any)
		if err != nil || !ok {
			return fmt.Errorf("%s needs a JSON object of arguments, such as {\"%s\": \"...\"}", what, exampleName(s))
		}
		for k, v := range obj {
			args[k] = v
		}
		return nil
	}
	if f.stdin {
		if file, ok := stdin.(*os.File); ok && isTerminal(file) {
			return nil, fmt.Errorf("--stdin reads the arguments from a pipe, such as: echo '{\"%s\": \"...\"}' | empirio run %s --stdin", exampleName(s), t.Name)
		}
		raw, err := io.ReadAll(io.LimitReader(stdin, maxMCPReply+1))
		if err != nil {
			return nil, err
		}
		if len(raw) > maxMCPReply {
			return nil, errors.New("--stdin: the arguments are too large")
		}
		if err := merge("--stdin", string(raw)); err != nil {
			return nil, err
		}
	}
	if f.argsJSON != "" {
		if err := merge("--json", f.argsJSON); err != nil {
			return nil, err
		}
	}
	takes := fmt.Sprintf("%s takes %s (see `empirio tools %s`)", t.Name, argumentList(s), t.Name)
	for _, pair := range pairs {
		name, text, ok := strings.Cut(pair, "=")
		if !ok || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("arguments are name=value, not %s; %s", quoteShort(pair), takes)
		}
		name = strings.TrimSpace(name)
		p, known := s.props[name]
		if !known && s.closed() {
			return nil, fmt.Errorf("%s has no argument %q; it %s", t.Name, name, strings.TrimPrefix(takes, t.Name+" "))
		}
		v, err := typeValue(name, text, p)
		if err != nil {
			return nil, err
		}
		args[name] = v
	}
	if s.closed() {
		for name := range args {
			if _, known := s.props[name]; !known {
				return nil, fmt.Errorf("%s has no argument %q; it %s", t.Name, name, strings.TrimPrefix(takes, t.Name+" "))
			}
		}
	}
	var missing []string
	for _, name := range s.Required {
		if v, ok := args[name]; !ok || v == nil {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%s needs %s; it %s", t.Name, strings.Join(missing, " and "), strings.TrimPrefix(takes, t.Name+" "))
	}
	// A tool that can wait for its job gets the time --wait allows, within the
	// tool's own limit, unless the arguments set it.
	if p, ok := s.props["wait_seconds"]; ok && p.kinds()["integer"] {
		if _, set := args["wait_seconds"]; !set {
			w := float64(f.wait)
			if p.Maximum != nil && w > *p.Maximum {
				w = *p.Maximum
			}
			if p.Minimum != nil && w < *p.Minimum {
				w = *p.Minimum
			}
			args["wait_seconds"] = int(w)
		}
	}
	return args, nil
}

func exampleName(s *schema) string {
	if len(s.Required) > 0 {
		return s.Required[0]
	}
	if len(s.names) > 0 {
		return s.names[0]
	}
	return "name"
}

// runCmd runs one tool and prints its result, saving the media it returns.
func runCmd(args []string) (int, error) {
	name, pairs, flags, err := parseRun(args)
	if err != nil {
		return 2, err
	}
	if name == "" {
		if flags.help {
			fmt.Fprintln(stdout, runUsage)
			fmt.Fprintln(stdout, "`empirio tools` lists the tools; `empirio tools <tool>` shows one tool's arguments.")
			return 0, nil
		}
		return 2, fmt.Errorf("%s; `empirio tools` lists the tools", runUsage)
	}
	s, err := connect()
	if err != nil {
		return 1, err
	}
	tools, err := s.tools()
	if err != nil {
		return 1, err
	}
	tool, ok := findTool(tools, name)
	if !ok {
		return 2, unknownTool(tools, name)
	}
	if flags.help {
		return 0, describeTool(tool)
	}
	sch, err := parseSchema(tool.InputSchema)
	if err != nil {
		return 1, fmt.Errorf("the arguments of %s could not be read: %w", tool.Name, err)
	}
	arguments, err := buildArguments(tool, sch, pairs, flags)
	if err != nil {
		return 2, err
	}
	r := &runner{s: s, tools: tools, tool: tool, flags: flags, arguments: arguments,
		deadline: time.Now().Add(time.Duration(flags.wait) * time.Second)}
	return r.run()
}

// runner carries one `empirio run` from the call to the saved files.
type runner struct {
	s         *mcpSession
	tools     []mcpTool
	tool      mcpTool
	flags     runFlags
	arguments map[string]any
	deadline  time.Time
}

func (r *runner) run() (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	res, raw, err := r.s.callTool(ctx, r.tool.Name, r.arguments)
	cancel()
	if err != nil {
		return 1, err
	}
	followed := false
	if job, ok := runningJob(res.Structured); ok && !res.IsError {
		if r.flags.wait == 0 {
			r.output(res, raw)
			notef("Job %s is %s. Check on it with `%s`.", job.id, job.status, r.checkCommand(job.id))
			return 0, nil
		}
		notef("Job %s is %s; waiting up to %s for it to finish.", job.id, job.status, seconds(r.flags.wait))
		res, raw, err = r.follow(job)
		if err != nil {
			return 1, err
		}
		if job, ok := runningJob(res.Structured); ok && !res.IsError {
			if r.flags.jsonOutput {
				r.output(res, raw)
			}
			return 1, fmt.Errorf("job %s is still %s after %s; check on it later with `%s`", job.id, job.status, seconds(r.flags.wait), r.checkCommand(job.id))
		}
		followed = true
	}
	if res.IsError {
		if r.flags.jsonOutput {
			r.output(res, raw)
		}
		return 1, errors.New(printable(firstNonEmpty(res.text(), r.tool.Name+" reported an error")))
	}
	r.output(res, raw)
	if r.flags.noDownload {
		return 0, nil
	}
	return r.save(res, !r.tool.Annotations.ReadOnly || followed)
}

// output prints the tool's text, or with --json-output its whole result.
func (r *runner) output(res toolResult, raw json.RawMessage) {
	if r.flags.jsonOutput {
		// The result as the server sent it, indented; JSON escapes control characters.
		var b bytes.Buffer
		if json.Indent(&b, raw, "", "  ") == nil {
			fmt.Fprintln(stdout, b.String())
		}
		return
	}
	if text := res.text(); text != "" {
		fmt.Fprintln(stdout, printable(text))
		return
	}
	if len(res.Structured) > 0 && string(res.Structured) != "null" {
		var v any
		if json.Unmarshal(res.Structured, &v) == nil {
			b, _ := json.MarshalIndent(v, "", "  ")
			fmt.Fprintln(stdout, printable(string(b)))
		}
	}
}

// save writes the result's media into --out and names each file.
func (r *runner) save(res toolResult, search bool) (int, error) {
	outputs := mediaOutputs(res, r.arguments, search)
	if len(outputs) == 0 {
		return 0, nil
	}
	if err := os.MkdirAll(r.flags.out, 0o755); err != nil {
		return 1, fmt.Errorf("could not create %s: %w", r.flags.out, err)
	}
	failed := 0
	for _, o := range outputs {
		ctx, cancel := context.WithTimeout(context.Background(), downloadClient.Timeout)
		path, size, skipped, err := saveOutput(ctx, o, r.flags.out, r.tool.Name)
		cancel()
		switch {
		case err != nil:
			failed++
			warnf("Could not save %s: %v", outputName(o.link, o.mimeType, r.tool.Name), err)
		case !skipped:
			okf("Saved %s (%s)", displayPath(path), sizeLabel(size))
		}
	}
	if failed > 0 {
		return 1, fmt.Errorf("%d of %d outputs could not be saved; the links above stay valid for a limited time", failed, len(outputs))
	}
	return 0, nil
}

// displayPath shows a saved file relative to the current folder when it is inside it.
func displayPath(p string) string {
	if wd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(wd, p); err == nil && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
			return rel
		}
	}
	return p
}

type job struct{ id, status string }

// runningJob reports a job the result says is still running.
func runningJob(structured json.RawMessage) (job, bool) {
	var v struct {
		JobID  string `json:"job_id"`
		Status string `json:"status"`
	}
	if len(structured) == 0 || json.Unmarshal(structured, &v) != nil || v.JobID == "" || v.Status == "" {
		return job{}, false
	}
	if finished[strings.ToLower(v.Status)] {
		return job{}, false
	}
	return job{v.JobID, v.Status}, true
}

// jobTool is the server's tool for reading a job by its id, when it lists one.
func (r *runner) jobTool() (mcpTool, *schema, bool) {
	t, ok := findTool(r.tools, "get_job")
	if !ok {
		return mcpTool{}, nil, false
	}
	s, err := parseSchema(t.InputSchema)
	if err != nil || s.props["job_id"] == nil {
		return mcpTool{}, nil, false
	}
	return t, s, true
}

func (r *runner) checkCommand(id string) string {
	if t, _, ok := r.jobTool(); ok {
		return "empirio run " + t.Name + " job_id=" + id
	}
	return "GET " + api.BaseURL() + "/v1/jobs/" + id
}

// follow reads the job until it finishes or the deadline passes: through the
// server's job tool, which waits for the job itself, else the jobs API.
func (r *runner) follow(j job) (toolResult, json.RawMessage, error) {
	t, sch, ok := r.jobTool()
	if !ok {
		return r.followAPI(j)
	}
	waits := sch.props["wait_seconds"] != nil && sch.props["wait_seconds"].kinds()["integer"]
	interval := jobPollInterval
	for {
		remaining := time.Until(r.deadline)
		args := map[string]any{"job_id": j.id}
		if waits {
			w := remaining.Seconds()
			if p := sch.props["wait_seconds"]; p.Maximum != nil && w > *p.Maximum {
				w = *p.Maximum
			}
			args["wait_seconds"] = max(int(w), 0)
		}
		started := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		res, raw, err := r.s.callTool(ctx, t.Name, args)
		cancel()
		if err != nil {
			return res, raw, err
		}
		if _, running := runningJob(res.Structured); !running || res.IsError || time.Until(r.deadline) < time.Second {
			return res, raw, nil
		}
		// Reads stay apart even when an answer comes back at once.
		if spent := time.Since(started); spent < interval {
			pause(min(interval-spent, time.Until(r.deadline)))
		}
		if !waits {
			interval = min(interval*3/2, 10*time.Second)
		}
	}
}

// followAPI reads the job from the jobs API and turns the finished job into a result.
func (r *runner) followAPI(j job) (toolResult, json.RawMessage, error) {
	client := api.New(Version)
	interval := jobPollInterval
	for {
		ctx, cancel := context.WithTimeout(context.Background(), listTimeout)
		raw, err := client.Job(ctx, r.s.key, j.id)
		cancel()
		if errors.Is(err, api.ErrInvalidKey) {
			return toolResult{}, nil, keyRefused(r.s.keySource)
		}
		if err != nil {
			return toolResult{}, nil, err
		}
		var view struct {
			Status string `json:"status"`
			Error  struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		json.Unmarshal(raw, &view)
		text, _ := json.MarshalIndent(json.RawMessage(raw), "", "  ")
		res := toolResult{Content: []contentBlock{{Type: "text", Text: string(text)}}, Structured: raw}
		status := strings.ToLower(view.Status)
		if status == "failed" || status == "error" {
			res.IsError = true
			res.Content[0].Text = firstNonEmpty(view.Error.Message, "job "+j.id+" failed")
		}
		if finished[status] || time.Until(r.deadline) < time.Second {
			return res, raw, nil
		}
		pause(min(interval, time.Until(r.deadline)))
		interval = min(interval*3/2, 10*time.Second)
	}
}

// pause waits; tests replace it.
var pause = func(d time.Duration) {
	if d > 0 {
		time.Sleep(d)
	}
}

// seconds says a number of seconds for people.
func seconds(n int) string {
	if n >= 60 && n%60 == 0 {
		return plural(n/60, "minute")
	}
	return plural(n, "second")
}
