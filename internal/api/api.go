// Package api talks to the EmpirioLabs AI API: the public model catalog and
// the key check.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/EmpirioLabs-AI/cli/internal/catalog"
)

// DefaultBaseURL is the public API.
const DefaultBaseURL = "https://api.empiriolabs.ai"

// BaseURL is the API origin, without a trailing slash or /v1.
// EMPIRIOLABS_BASE_URL overrides it.
func BaseURL() string {
	if v := strings.TrimSpace(os.Getenv("EMPIRIOLABS_BASE_URL")); v != "" {
		v = strings.TrimRight(v, "/")
		return strings.TrimSuffix(v, "/v1")
	}
	return DefaultBaseURL
}

// CheckOverride refuses an address in the environment variable name that
// would send the key or a download in the clear: it must use https, except
// on this computer.
func CheckOverride(name string) error {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("%s is not an address: %q", name, raw)
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" {
		host := u.Hostname()
		if ip := net.ParseIP(host); host == "localhost" || (ip != nil && ip.IsLoopback()) {
			return nil
		}
	}
	return fmt.Errorf("%s must start with https:// (http:// only for this computer)", name)
}

// ErrInvalidKey means the API refused the key.
var ErrInvalidKey = errors.New("the API key was not accepted")

// Client calls the API.
type Client struct {
	BaseURL   string
	UserAgent string
	HTTP      *http.Client
}

// New returns a client for BaseURL().
func New(version string) *Client {
	return &Client{
		BaseURL:   BaseURL(),
		UserAgent: "empirio-cli/" + version,
		HTTP:      &http.Client{Timeout: 60 * time.Second},
	}
}

func (c *Client) get(ctx context.Context, path, key string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	return c.HTTP.Do(req)
}

// Models reads the live catalog of available models.
func (c *Client) Models(ctx context.Context) (*catalog.Catalog, error) {
	resp, err := c.get(ctx, "/v1/models?available=true", "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("model list: HTTP %d", resp.StatusCode)
	}
	var cat catalog.Catalog
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(&cat); err != nil {
		return nil, fmt.Errorf("model list: %w", err)
	}
	if len(cat.Models) == 0 {
		return nil, errors.New("model list: no models")
	}
	cat.FetchedAt = time.Now().UTC()
	return &cat, nil
}

// Balance is the account's credit balance.
type Balance struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

// Account checks a key and reads the balance of the account it belongs to.
// The model list is public, so it cannot tell a good key from a bad one.
func (c *Client) Account(ctx context.Context, key string) (Balance, error) {
	resp, err := c.get(ctx, "/v1/account/usage?limit=1", key)
	if err != nil {
		return Balance{}, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return Balance{}, ErrInvalidKey
	case resp.StatusCode != http.StatusOK:
		return Balance{}, fmt.Errorf("key check: HTTP %d", resp.StatusCode)
	}
	var body struct {
		Balance Balance `json:"balance"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&body); err != nil {
		return Balance{}, fmt.Errorf("key check: %w", err)
	}
	return body.Balance, nil
}
