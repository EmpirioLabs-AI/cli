package app

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/EmpirioLabs-AI/cli/internal/config"
	"github.com/EmpirioLabs-AI/cli/internal/release"
)

// Releases are published on GitHub with a SHA256SUMS file; EMPIRIO_RELEASES
// points at a mirror serving the same layout.
const releaseRepo = "EmpirioLabs-AI/cli"

func releasesBase() string {
	if v := strings.TrimSpace(os.Getenv("EMPIRIO_RELEASES")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "https://github.com/" + releaseRepo + "/releases"
}

var httpClient = &http.Client{Timeout: 5 * time.Minute}

// latestRelease reads the newest version from the redirect of /releases/latest.
func latestRelease(timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, releasesBase()+"/latest", nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	i := strings.LastIndex(loc, "/v")
	if i < 0 {
		return "", errors.New("no release found")
	}
	v := strings.TrimPrefix(loc[i+1:], "v")
	if !validVersion(v) {
		return "", errors.New("no release found")
	}
	return v, nil
}

var versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$`)

// validVersion accepts a release version such as 1.2.3 or 1.2.3-rc.1.
func validVersion(v string) bool { return versionPattern.MatchString(v) }

func parseVersion(v string) [3]int {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	v, _, _ = strings.Cut(v, "-")
	for i, part := range strings.SplitN(v, ".", 3) {
		n, _ := strconv.Atoi(part)
		out[i] = n
	}
	return out
}

// newer reports whether version a is newer than b.
func newer(a, b string) bool {
	if strings.Contains(b, "-dev") {
		return false
	}
	x, y := parseVersion(a), parseVersion(b)
	for i := range x {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}
	return false
}

type state struct {
	CheckedAt time.Time `json:"update_checked_at"`
	Latest    string    `json:"latest_version"`
}

func statePath() string { return filepath.Join(config.Dir(), "state.json") }

// checkForUpdate says once a day, never blocking a launch for long, when a newer release exists.
func checkForUpdate() {
	if os.Getenv("EMPIRIO_NO_UPDATE_CHECK") != "" || strings.Contains(Version, "-dev") || !isTerminal(os.Stderr) {
		return
	}
	var s state
	if b, err := os.ReadFile(statePath()); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	if timeSince(s.CheckedAt) > 24*time.Hour {
		if latest, err := latestRelease(1500 * time.Millisecond); err == nil {
			s = state{CheckedAt: time.Now().UTC(), Latest: latest}
			if b, err := json.Marshal(s); err == nil {
				_ = config.WriteFileAtomic(statePath(), b, 0o600)
			}
		}
	}
	if s.Latest != "" && newer(s.Latest, Version) {
		notef("empirio %s is available: run `empirio update`", s.Latest)
	}
}

// assetName is this platform's release archive. Names carry no version, so
// releases/latest/download/<name> always resolves to the newest release.
func assetName() string {
	ext := "tar.gz"
	if runtime.GOOS == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("empirio_%s_%s.%s", runtime.GOOS, runtime.GOARCH, ext)
}

func download(url string, limit int64) ([]byte, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

func update(args []string) error {
	target := ""
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			return fmt.Errorf("unknown flag %q (usage: empirio update [<version>])", a)
		}
		target = strings.TrimPrefix(a, "v")
		if !validVersion(target) {
			return fmt.Errorf("%q is not a release version such as 0.2.0", a)
		}
	}
	if target == "" {
		latest, err := latestRelease(20 * time.Second)
		if err != nil {
			return fmt.Errorf("could not find the latest release: %w", err)
		}
		if !newer(latest, Version) && !strings.Contains(Version, "-dev") {
			okf("empirio %s is the latest version.", Version)
			return nil
		}
		target = latest
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	base := releasesBase() + "/download/v" + target + "/"
	name := assetName()
	fmt.Fprintf(stderr, "Downloading empirio %s (%s/%s)...\n", target, runtime.GOOS, runtime.GOARCH)
	sums, err := download(base+"SHA256SUMS", 1<<20)
	if err != nil {
		return err
	}
	// Only a SHA256SUMS signed by the release key is trusted; the checksum
	// then ties the archive to it.
	sig, err := download(base+"SHA256SUMS.sig", 64<<10)
	if err != nil {
		return fmt.Errorf("the release has no signature, so nothing was changed: %w", err)
	}
	keys, err := release.TrustedKeys()
	if err != nil {
		return err
	}
	if err := release.Verify(sums, sig, keys); err != nil {
		return fmt.Errorf("%v; nothing was changed", err)
	}
	archive, err := download(base+name, 200<<20)
	if err != nil {
		return err
	}
	if err := verifySum(sums, name, archive); err != nil {
		return err
	}
	binary, err := extractBinary(name, archive)
	if err != nil {
		return err
	}
	if err := replaceSelf(self, binary); err != nil {
		return fmt.Errorf("could not replace %s: %w", self, err)
	}
	okf("Updated to empirio %s.", target)
	return nil
}

func verifySum(sums []byte, name string, data []byte) error {
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	scanner := bufio.NewScanner(bytes.NewReader(sums))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			if strings.EqualFold(fields[0], got) {
				return nil
			}
			return errors.New("checksum mismatch for " + name + "; nothing was changed")
		}
	}
	return errors.New(name + " is not listed in SHA256SUMS; nothing was changed")
}

func extractBinary(name string, archive []byte) ([]byte, error) {
	want := "empirio"
	if runtime.GOOS == "windows" {
		want = "empirio.exe"
	}
	if strings.HasSuffix(name, ".zip") {
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if filepath.Base(f.Name) == want {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(io.LimitReader(rc, 200<<20))
			}
		}
		return nil, errors.New(want + " is missing from the release archive")
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, errors.New(want + " is missing from the release archive")
		}
		if err != nil {
			return nil, err
		}
		if filepath.Base(h.Name) == want && h.Typeflag == tar.TypeReg {
			return io.ReadAll(io.LimitReader(tr, 200<<20))
		}
	}
}

// replaceSelf swaps the running binary for a new one. Windows cannot overwrite
// a running executable but can rename it, so the old one is moved aside first.
func replaceSelf(self string, binary []byte) error {
	dir := filepath.Dir(self)
	tmp, err := os.CreateTemp(dir, ".empirio-update-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(binary); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	tmp.Close()
	if err := os.Chmod(tmpName, 0o755); err != nil && runtime.GOOS != "windows" {
		os.Remove(tmpName)
		return err
	}
	if runtime.GOOS == "windows" {
		old := self + ".old"
		os.Remove(old)
		if err := os.Rename(self, old); err != nil {
			os.Remove(tmpName)
			return err
		}
		if err := os.Rename(tmpName, self); err != nil {
			os.Rename(old, self)
			return err
		}
		return nil
	}
	return os.Rename(tmpName, self)
}

// removeOldBinary deletes what a Windows update moved aside.
func removeOldBinary() {
	if runtime.GOOS != "windows" {
		return
	}
	if self, err := os.Executable(); err == nil {
		os.Remove(self + ".old")
	}
}
