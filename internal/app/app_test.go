package app

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/EmpirioLabs-AI/cli/internal/agents"
)

const testKey = "sk-empiriolabs-TESTKEYTESTKEYTESTKEYTESTKEY0123456789"

func TestParseRedirect(t *testing.T) {
	const state = "s7ate"
	var bad unreadable
	cases := []struct {
		in, code string
		unread   bool
		fails    bool
	}{
		{in: "http://127.0.0.1:5123/callback?code=abc&state=s7ate", code: "abc"},
		{in: "  http://127.0.0.1:5123/callback?state=s7ate&code=abc#frag \n", code: "abc"},
		{in: "code=abc&state=s7ate", code: "abc"},
		{in: "abc123", code: "abc123"},
		{in: "http://127.0.0.1:5123/callback?code=abc&state=other", unread: true},
		{in: "http://127.0.0.1:5123/callback?code=abc", unread: true},
		{in: "http://127.0.0.1:5123/callback?error=access_denied", unread: true},
		{in: "http://127.0.0.1:5123/callback?state=s7ate", unread: true},
		{in: "", unread: true},
		{in: "http://127.0.0.1:5123/callback?error=access_denied&state=s7ate", fails: true},
		{in: "http://127.0.0.1:5123/callback?error=server_error&error_description=Try+again&state=s7ate", fails: true},
	}
	for _, c := range cases {
		code, err := parseRedirect(c.in, state)
		switch {
		case c.unread:
			if !errors.As(err, &bad) {
				t.Errorf("%q: want an unreadable address, got %q %v", c.in, code, err)
			}
		case c.fails:
			if err == nil || errors.As(err, &bad) {
				t.Errorf("%q: want a finished, failed sign-in, got %v", c.in, err)
			}
		default:
			if err != nil || code != c.code {
				t.Errorf("%q: got %q %v, want %q", c.in, code, err, c.code)
			}
		}
	}
}

func TestCallbackDeliversOnlyThisSignInOnce(t *testing.T) {
	results := make(chan callback, 1)
	h := callbackHandler("127.0.0.1:5123", "s7ate", results)
	get := func(host, query string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "http://"+host+"/callback?"+query, nil)
		req.Host = host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	// A page on another origin cannot reach the handler through a rebound name.
	if w := get("evil.example:5123", "code=abc&state=s7ate"); w.Code != http.StatusNotFound {
		t.Fatalf("other host: %d", w.Code)
	}
	// Without this sign-in's state nothing is delivered, so nothing is cancelled.
	if w := get("127.0.0.1:5123", "error=access_denied"); w.Code != http.StatusBadRequest {
		t.Fatalf("forged cancel: %d", w.Code)
	}
	select {
	case got := <-results:
		t.Fatalf("delivered %+v", got)
	default:
	}
	done := make(chan *httptest.ResponseRecorder)
	go func() { done <- get("127.0.0.1:5123", "code=abc&state=s7ate") }()
	got := <-results
	if got.code != "abc" || got.err != nil {
		t.Fatalf("delivered %+v", got)
	}
	got.reply <- nil
	w := <-done
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "You are signed in") {
		t.Fatalf("page: %d %s", w.Code, w.Body.String())
	}
	for _, header := range []string{"Content-Security-Policy", "Cache-Control", "Referrer-Policy"} {
		if w.Header().Get(header) == "" {
			t.Fatalf("missing %s", header)
		}
	}
	if w := get("127.0.0.1:5123", "code=again&state=s7ate"); w.Code != http.StatusConflict {
		t.Fatalf("second delivery: %d", w.Code)
	}
}

func TestCallbackPageShowsAFailedExchange(t *testing.T) {
	results := make(chan callback, 1)
	h := callbackHandler("127.0.0.1:5123", "s7ate", results)
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:5123/callback?code=abc&state=s7ate", nil)
	w := httptest.NewRecorder()
	go func() {
		got := <-results
		got.reply <- errors.New("the sign-in was not completed: <b>expired</b>")
	}()
	h.ServeHTTP(w, req)
	body := w.Body.String()
	if w.Code != http.StatusBadRequest || !strings.Contains(body, "Sign-in did not finish") || strings.Contains(body, "<b>") {
		t.Fatalf("page: %d %s", w.Code, body)
	}
}

func TestPKCEChallengeIsS256(t *testing.T) {
	// RFC 7636 appendix B.
	if got := pkceChallenge("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"); got != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Fatal(got)
	}
	if a, b := randomString(32), randomString(32); a == b || len(a) < 43 {
		t.Fatal("random strings")
	}
}

func TestRenderMasksTheKey(t *testing.T) {
	p := agents.Plan{
		Binary:  "/usr/bin/claude",
		Args:    []string{"--settings", "/tmp/run/claude.json", "-p", "it's"},
		Env:     map[string]string{"EMPIRIOLABS_API_KEY": testKey, "OTHER": "a b"},
		Unset:   []string{"ANTHROPIC_API_KEY"},
		Files:   []agents.File{{Path: "/tmp/run/claude.json", Data: []byte(testKey)}},
		Secrets: []string{testKey},
	}
	out := render(p)
	if strings.Contains(out, testKey) {
		t.Fatalf("the key is printed:\n%s", out)
	}
	if !strings.Contains(out, "sk-empiriolabs-TEST...6789") || !strings.Contains(out, "ANTHROPIC_API_KEY") {
		t.Fatalf("render:\n%s", out)
	}
}

func TestMergedEnvReplacesAndUnsets(t *testing.T) {
	t.Setenv("EMPIRIO_TEST_KEEP", "1")
	t.Setenv("EMPIRIO_TEST_GONE", "1")
	t.Setenv("EMPIRIO_TEST_OVER", "old")
	env := mergedEnv(agents.Plan{
		Env:   map[string]string{"EMPIRIO_TEST_OVER": "new"},
		Unset: []string{"EMPIRIO_TEST_GONE"},
	})
	seen := map[string]int{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(strings.ToUpper(k), "EMPIRIO_TEST_") {
			seen[strings.ToUpper(k)+"="+v]++
		}
	}
	if seen["EMPIRIO_TEST_KEEP=1"] != 1 || seen["EMPIRIO_TEST_OVER=new"] != 1 || len(seen) != 2 {
		t.Fatalf("env: %v", seen)
	}
	if runtime.GOOS == "windows" {
		// Windows names are case-insensitive: a differently cased copy must not survive.
		t.Setenv("Empirio_Test_Case", "old")
		env = mergedEnv(agents.Plan{Env: map[string]string{"EMPIRIO_TEST_CASE": "new"}})
		n := 0
		for _, kv := range env {
			if strings.HasPrefix(strings.ToUpper(kv), "EMPIRIO_TEST_CASE=") {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("%d copies", n)
		}
	}
}

func TestTakeFlags(t *testing.T) {
	var f launchFlags
	rest, err := takeFlags([]string{"-m", "glm-5-3", "--fast-model=flash", "--print-env", "--", "-p", "hi"}, &f)
	if err != nil || f.model != "glm-5-3" || f.fast != "flash" || !f.printEnv || strings.Join(rest, " ") != "-p hi" {
		t.Fatalf("%+v %v %v", f, rest, err)
	}
	f = launchFlags{}
	rest, _ = takeFlags([]string{"--model=x", "exec", "-m", "keep"}, &f)
	if f.model != "x" || strings.Join(rest, " ") != "exec -m keep" {
		t.Fatalf("stops at the first agent argument: %+v %v", f, rest)
	}
	if _, err := takeFlags([]string{"-m"}, &f); err == nil {
		t.Fatal("-m without a value")
	}
	if _, err := takeFlags([]string{"-m", "--print-env"}, &f); err == nil {
		t.Fatal("-m followed by a flag")
	}
}

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.2.0", "0.1.9", true},
		{"v1.0.0", "0.9.9", true},
		{"0.1.10", "0.1.9", true},
		{"0.1.0", "0.1.0", false},
		{"0.1.0", "0.2.0", false},
		{"9.9.9", "0.0.0-dev", false},
		{"1.0.0", "1.0.0-rc.1", false},
	}
	for _, c := range cases {
		if got := newer(c.a, c.b); got != c.want {
			t.Errorf("newer(%s, %s) = %v", c.a, c.b, got)
		}
	}
}

func TestVerifySum(t *testing.T) {
	data := []byte("release bytes")
	sum := sha256.Sum256(data)
	hexSum := hex.EncodeToString(sum[:])
	sums := []byte("0000  empirio_linux_amd64.tar.gz\n" + strings.ToUpper(hexSum) + " *empirio_windows_amd64.zip\n")
	if err := verifySum(sums, "empirio_windows_amd64.zip", data); err != nil {
		t.Fatal(err)
	}
	if err := verifySum(sums, "empirio_linux_amd64.tar.gz", data); err == nil {
		t.Fatal("a mismatch passed")
	}
	if err := verifySum(sums, "empirio_darwin_arm64.tar.gz", data); err == nil {
		t.Fatal("an unlisted archive passed")
	}
}

func TestExtractBinaryFindsOnlyTheExecutable(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{"README.md": "readme", "empirio_0.1.0/empirio.exe": "exe", "empirio_0.1.0/empirio": "elf"} {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	zw.Close()
	got, err := extractBinary("x.zip", buf.Bytes())
	want := "elf"
	if runtime.GOOS == "windows" {
		want = "exe"
	}
	if err != nil || string(got) != want {
		t.Fatalf("got %q %v", got, err)
	}
}

func TestAssetNameHasNoVersion(t *testing.T) {
	if name := assetName(); !strings.HasPrefix(name, "empirio_"+runtime.GOOS+"_"+runtime.GOARCH+".") {
		t.Fatal(name)
	}
}

func TestValidVersion(t *testing.T) {
	for _, v := range []string{"0.1.0", "1.20.3", "1.0.0-rc.1"} {
		if !validVersion(v) {
			t.Errorf("%s rejected", v)
		}
	}
	for _, v := range []string{"", "1.0", "../1.0.0", "1.0.0/../../x", "1.0.0 ", "latest"} {
		if validVersion(v) {
			t.Errorf("%q accepted", v)
		}
	}
}

func TestAgentRecordsAreComplete(t *testing.T) {
	records := agentRecords()
	if len(records) < 10 {
		t.Fatalf("%d agents", len(records))
	}
	names := map[string]bool{}
	for _, r := range records {
		if names[r.Name] {
			t.Fatalf("duplicate %s", r.Name)
		}
		names[r.Name] = true
		if r.Command != "empirio "+r.Name || r.Title == "" || r.APIName == "" || !strings.HasPrefix(r.Docs, "https://") {
			t.Fatalf("record: %+v", r)
		}
		if len(r.Install) == 0 {
			t.Fatalf("%s has no install command", r.Name)
		}
		for _, cmd := range r.Install {
			if strings.Contains(cmd, "empiriolabs.ai") {
				t.Fatalf("%s: an install command names our hosts: %s", r.Name, cmd)
			}
		}
	}
}

func TestConfigNeverLeavesTheKeyWorldReadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes do not apply; the file lives in the user's own profile")
	}
	dir := t.TempDir()
	t.Setenv("EMPIRIO_CONFIG_DIR", dir)
	path, err := runDir()
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("run directory mode %v", info.Mode().Perm())
	}
}

func TestAgoAndPlural(t *testing.T) {
	now := time.Now()
	timeSince = func(t time.Time) time.Duration { return now.Sub(t) }
	defer func() { timeSince = time.Since }()
	if ago(now.Add(-90*time.Minute)) != "1 hour ago" || ago(now.Add(-72*time.Hour)) != "3 days ago" || ago(now) != "just now" {
		t.Fatal(ago(now.Add(-90 * time.Minute)))
	}
}

func TestOverridesMustNotSendTheKeyInTheClear(t *testing.T) {
	for value, ok := range map[string]bool{
		"":                           true,
		"https://api.example.test":   true,
		"http://127.0.0.1:8790/v1":   true,
		"http://localhost:3000":      true,
		"http://[::1]:8080":          true,
		"http://api.example.test":    false,
		"http://127.0.0.1.evil.test": false,
		"ftp://example.test":         false,
		"not a url":                  false,
	} {
		t.Setenv("EMPIRIOLABS_BASE_URL", value)
		if err := checkOverrides(); (err == nil) != ok {
			t.Errorf("%q: %v", value, err)
		}
	}
}
