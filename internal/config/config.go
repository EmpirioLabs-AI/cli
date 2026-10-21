// Package config stores the CLI's own settings and the API key.
//
// Everything lives in one directory the CLI owns: config.json (key and default
// models, mode 0600) and models.json (the cached catalog). No agent's own
// configuration file is ever written.
package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// KeyEnv names the environment variable that holds an API key. It wins over a saved key.
const KeyEnv = "EMPIRIOLABS_API_KEY"

// ErrNoKey means neither the environment nor the saved config has a key.
var ErrNoKey = errors.New("no EmpirioLabs AI API key: run `empirio login`, or set " + KeyEnv)

// Config is what config.json holds.
type Config struct {
	APIKey string `json:"api_key,omitempty"`
	// KeySource is "browser" for a connection made by signing in, which
	// logout disconnects, or "key" for a key pasted from the API keys page.
	KeySource string `json:"key_source,omitempty"`
	Model     string `json:"model,omitempty"`
	FastModel string `json:"fast_model,omitempty"`
}

// Dir is the CLI's own directory: EMPIRIO_CONFIG_DIR, else
// %APPDATA%\empirio on Windows and $XDG_CONFIG_HOME/empirio (~/.config/empirio)
// elsewhere, macOS included, so every guide can name one path.
func Dir() string {
	if d := strings.TrimSpace(os.Getenv("EMPIRIO_CONFIG_DIR")); d != "" {
		return d
	}
	if runtime.GOOS == "windows" {
		if d := os.Getenv("APPDATA"); d != "" {
			return filepath.Join(d, "empirio")
		}
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "empirio")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "empirio")
}

// Path is config.json.
func Path() string { return filepath.Join(Dir(), "config.json") }

// Load reads config.json; a missing file is an empty config.
func Load() (Config, error) {
	var c Config
	b, err := os.ReadFile(Path())
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, errors.New(Path() + " is not valid JSON; fix it or remove it and run `empirio login` again")
	}
	return c, nil
}

// Save writes config.json atomically, readable only by the user.
func Save(c Config) error {
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(Path(), append(b, '\n'), 0o600)
}

// WriteFileAtomic replaces path with data through a temporary file in the same directory.
func WriteFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(mode); err != nil && runtime.GOOS != "windows" {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return replace(name, path)
}

// Key returns the API key and where it came from ("environment" or the config path).
func Key() (key, source string, err error) {
	if k := strings.TrimSpace(os.Getenv(KeyEnv)); k != "" {
		return k, KeyEnv, nil
	}
	c, err := Load()
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(c.APIKey) == "" {
		return "", "", ErrNoKey
	}
	return strings.TrimSpace(c.APIKey), Path(), nil
}

// Mask shows enough of a key to recognise it and no more.
func Mask(key string) string {
	key = strings.TrimSpace(key)
	const prefix = "sk-empiriolabs-"
	if strings.HasPrefix(key, prefix) && len(key) > len(prefix)+8 {
		return prefix + key[len(prefix):len(prefix)+4] + "..." + key[len(key)-4:]
	}
	if len(key) <= 12 {
		return "****"
	}
	return key[:4] + "..." + key[len(key)-4:]
}
