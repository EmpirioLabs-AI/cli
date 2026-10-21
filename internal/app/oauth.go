package app

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/EmpirioLabs-AI/cli/internal/api"
)

// Browser sign-in: OAuth 2.1 authorization code with PKCE and a loopback
// redirect (RFC 8252). The CLI is a published client of the EmpirioLabs AI
// authorization server; approving it in the browser gives this computer a
// dedicated key that appears under Settings > Connected applications, where
// it can be disconnected at any time.
const (
	defaultIssuer = "https://platform.empiriolabs.ai"
	oauthClientID = "empiriolabs-cli"
	signInTimeout = 10 * time.Minute
)

func issuer() string {
	if v := strings.TrimSpace(os.Getenv("EMPIRIO_OAUTH_ISSUER")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return defaultIssuer
}

func randomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // the system's randomness source failed; nothing below is safe without it
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// unreadable is a pasted or delivered address that does not finish this
// sign-in. The sign-in keeps waiting: such an address can come from a typo,
// an older sign-in or another page, so it never ends one.
type unreadable string

func (e unreadable) Error() string { return string(e) }

type callback struct {
	code  string
	err   error
	reply chan error // the exchange's outcome, for the browser page
}

// parseRedirect reads the code from the address the browser was sent to, or
// takes a bare code. An address must carry this sign-in's state.
func parseRedirect(input, state string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", unreadable("nothing was pasted")
	}
	if !strings.ContainsAny(input, "?=&/") {
		return input, nil
	}
	raw := input
	if i := strings.Index(raw, "?"); i >= 0 {
		raw = raw[i+1:]
	}
	if i := strings.Index(raw, "#"); i >= 0 {
		raw = raw[:i]
	}
	q, err := url.ParseQuery(raw)
	if err != nil {
		return "", unreadable("that address could not be read")
	}
	if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(state)) != 1 {
		return "", unreadable("that address belongs to a different sign-in")
	}
	if e := q.Get("error"); e != "" {
		if e == "access_denied" {
			return "", errors.New("the sign-in was cancelled in the browser")
		}
		return "", fmt.Errorf("the sign-in failed: %s", firstNonEmpty(q.Get("error_description"), e))
	}
	code := q.Get("code")
	if code == "" {
		return "", unreadable("that address has no sign-in code")
	}
	return code, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// sentence turns an error into a sentence for the browser page.
func sentence(err error) string {
	s := err.Error()
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:] + "."
}

const callbackPage = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s</title><style>body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;background:#05070d;color:#e6edf3;font:16px/1.5 system-ui,-apple-system,"Segoe UI",sans-serif}
main{max-width:28rem;padding:2rem;border:1px solid rgba(255,255,255,.08);border-radius:1rem;background:rgba(255,255,255,.02)}h1{margin:0 0 .5rem;font-size:1.25rem}p{margin:0;color:#9aa4b2}</style></head>
<body><main><h1>%s</h1><p>%s</p></main></body></html>`

func writePage(w http.ResponseWriter, status int, title, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	fmt.Fprintf(w, callbackPage, html.EscapeString(title), html.EscapeString(title), html.EscapeString(message))
}

// openBrowser asks the system to open url, reporting whether it could try.
func openBrowser(target string) bool {
	if os.Getenv("EMPIRIO_NO_BROWSER") != "" {
		return false
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	case "darwin":
		cmd = exec.Command("open", target)
	default:
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return false
		}
		cmd = exec.Command("xdg-open", target)
	}
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	return cmd.Start() == nil
}

// remoteSession reports a shell on another computer, where a browser opened
// here would not be the user's.
func remoteSession() bool {
	return os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != ""
}

// callbackHandler serves the loopback redirect. Only an address carrying this
// sign-in's state is delivered, once, and the page shows how the sign-in ended.
func callbackHandler(host, state string, results chan<- callback) http.Handler {
	var delivered atomic.Bool
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" || r.Method != http.MethodGet || r.Host != host {
			http.NotFound(w, r)
			return
		}
		code, err := parseRedirect("?"+r.URL.RawQuery, state)
		var bad unreadable
		if errors.As(err, &bad) {
			writePage(w, http.StatusBadRequest, "Sign-in did not finish", "This address is not for the sign-in in your terminal. Run empirio login again.")
			return
		}
		if !delivered.CompareAndSwap(false, true) {
			writePage(w, http.StatusConflict, "Sign-in already finished", "Return to your terminal. You can close this tab.")
			return
		}
		reply := make(chan error, 1)
		results <- callback{code: code, err: err, reply: reply}
		select {
		case err = <-reply:
		case <-r.Context().Done():
			return
		case <-time.After(time.Minute):
			writePage(w, http.StatusOK, "Return to your terminal", "The sign-in continues there. You can close this tab.")
			return
		}
		if err != nil {
			writePage(w, http.StatusBadRequest, "Sign-in did not finish", sentence(err)+" Return to your terminal.")
			return
		}
		writePage(w, http.StatusOK, "You are signed in", "Return to your terminal. You can close this tab.")
	})
}

// signInWithBrowser runs the whole flow and returns the connection's key.
//
// With a browser on this computer it waits only for the loopback redirect and
// never reads the terminal, so nothing is left reading it when an agent starts.
// Without one (noBrowser, or a remote shell) the user opens the address on
// any device and pastes back the address the browser ends on.
func signInWithBrowser(noBrowser bool) (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("could not open a local port for the sign-in: %w", err)
	}
	defer listener.Close()
	host := listener.Addr().String()
	redirectURI := "http://" + host + "/callback"
	verifier := randomString(48)
	state := randomString(24)
	query := url.Values{
		"response_type":         {"code"},
		"client_id":             {oauthClientID},
		"redirect_uri":          {redirectURI},
		"code_challenge":        {pkceChallenge(verifier)},
		"code_challenge_method": {"S256"},
		"state":                 {state},
		"scope":                 {"api"},
		"resource":              {api.BaseURL()},
	}
	authorizeURL := issuer() + "/dashboard/oauth/authorize?" + query.Encode()

	results := make(chan callback, 1)
	server := &http.Server{Handler: callbackHandler(host, state, results), ReadHeaderTimeout: 10 * time.Second}
	go server.Serve(listener)
	defer func() {
		// Let the browser page finish before the server goes.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()

	pasting := noBrowser || remoteSession() || !openBrowser(authorizeURL)
	if pasting {
		fmt.Fprintln(stderr, "Open this address in a browser on any device to sign in to EmpirioLabs AI:")
		fmt.Fprintln(stderr, "  "+accent(authorizeURL))
		fmt.Fprintln(stderr, dim("After you approve, copy the address the browser ends on (the page may not load) and paste it here."))
	} else {
		fmt.Fprintln(stderr, "Opening your browser to sign in to EmpirioLabs AI. If it does not open, visit:")
		fmt.Fprintln(stderr, "  "+accent(authorizeURL))
		fmt.Fprintln(stderr, dim("Waiting for you to approve. Signing in from another device? Press Ctrl+C and run `empirio login --no-browser`."))
	}

	var finished atomic.Bool
	pasted := make(chan callback, 1)
	readerDone := make(chan struct{})
	if pasting {
		go func() {
			defer close(readerDone)
			in := bufio.NewReader(os.Stdin)
			for {
				fmt.Fprint(stderr, "Address: ")
				line, err := in.ReadString('\n')
				if finished.Load() {
					return
				}
				if err != nil && strings.TrimSpace(line) == "" {
					pasted <- callback{err: errors.New("the sign-in was cancelled")}
					return
				}
				code, perr := parseRedirect(line, state)
				var bad unreadable
				if errors.As(perr, &bad) {
					warnf("%s; paste the whole address from this sign-in", perr)
					continue
				}
				pasted <- callback{code: code, err: perr}
				return
			}
		}()
	}

	var got callback
	select {
	case got = <-results:
		if pasting {
			// The terminal is mid-read; finish that line so nothing is left
			// reading it when an agent starts.
			finished.Store(true)
			fmt.Fprintln(stderr)
			okf("Approved in the browser. Press Enter to continue.")
			<-readerDone
		}
	case got = <-pasted:
	case <-time.After(signInTimeout):
		return "", errors.New("the sign-in timed out; run `empirio login` again")
	}
	key, err := "", got.err
	if err == nil {
		key, err = exchangeCode(got.code, redirectURI, verifier)
	}
	if got.reply != nil {
		got.reply <- err
	}
	return key, err
}

func exchangeCode(code, redirectURI, verifier string) (string, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {oauthClientID},
		"code_verifier": {verifier},
		"resource":      {api.BaseURL()},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, issuer()+"/api/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "empirio-cli/"+Version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("could not finish the sign-in: %w", err)
	}
	defer resp.Body.Close()
	var body struct {
		AccessToken      string `json:"access_token"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", fmt.Errorf("could not finish the sign-in: HTTP %d", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the sign-in was not completed: %s", firstNonEmpty(body.ErrorDescription, body.Error, fmt.Sprintf("HTTP %d", resp.StatusCode)))
	}
	if !strings.HasPrefix(body.AccessToken, "sk-empiriolabs-") {
		return "", errors.New("the sign-in returned no key; run `empirio login --key` to paste one instead")
	}
	return body.AccessToken, nil
}

// revokeConnection ends a connection made by browser sign-in (best effort).
func revokeConnection(key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	form := url.Values{"token": {key}, "client_id": {oauthClientID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, issuer()+"/api/oauth/revoke", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "empirio-cli/"+Version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}
