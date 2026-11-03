package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// A tool's media outputs (images, video, audio, 3D models) are saved as files.
// The server names a result's outputs as resource links; a result without
// them is searched for links to media files. Only https links are fetched,
// never with the key, and never more than maxDownload bytes each.
const maxOutputs = 20

var maxDownload int64 = 2 << 30

// downloadClient fetches output files. It sends no credentials and follows a
// redirect only to another https address.
var downloadClient = &http.Client{
	Timeout: 30 * time.Minute,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" {
			return errors.New("the download was redirected away from https")
		}
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		return nil
	},
}

// mediaExtensions are the file types saved from a link without asking the server.
var mediaExtensions = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".webp": true, ".gif": true, ".avif": true, ".bmp": true,
	".tif": true, ".tiff": true, ".heic": true, ".heif": true, ".svg": true,
	".mp4": true, ".webm": true, ".mov": true, ".mkv": true, ".m4v": true, ".avi": true,
	".mp3": true, ".wav": true, ".ogg": true, ".oga": true, ".opus": true, ".flac": true, ".aac": true, ".m4a": true,
	".glb": true, ".gltf": true, ".obj": true, ".fbx": true, ".stl": true, ".ply": true, ".usdz": true, ".3mf": true,
}

// typeExtensions names a file whose link has no extension of its own. Go's
// mime table is not used: on Windows it comes from the registry and varies.
var typeExtensions = map[string]string{
	"image/png": ".png", "image/jpeg": ".jpg", "image/webp": ".webp", "image/gif": ".gif", "image/avif": ".avif",
	"image/bmp": ".bmp", "image/tiff": ".tiff", "image/heic": ".heic", "image/heif": ".heif", "image/svg+xml": ".svg",
	"video/mp4": ".mp4", "video/webm": ".webm", "video/quicktime": ".mov", "video/x-matroska": ".mkv",
	"audio/mpeg": ".mp3", "audio/mp3": ".mp3", "audio/wav": ".wav", "audio/x-wav": ".wav", "audio/wave": ".wav",
	"audio/ogg": ".ogg", "audio/opus": ".opus", "audio/flac": ".flac", "audio/aac": ".aac", "audio/mp4": ".m4a",
	"audio/webm": ".webm", "model/gltf-binary": ".glb", "model/gltf+json": ".gltf", "model/obj": ".obj",
	"model/stl": ".stl", "model/vnd.usdz+zip": ".usdz", "model/3mf": ".3mf",
}

// isMediaType reports a media file's content type.
func isMediaType(contentType string) bool {
	t, _, _ := mime.ParseMediaType(contentType)
	for _, prefix := range []string{"image/", "video/", "audio/", "model/"} {
		if strings.HasPrefix(t, prefix) {
			return true
		}
	}
	return false
}

func mediaExtension(u *url.URL) string {
	ext := strings.ToLower(path.Ext(u.Path))
	if mediaExtensions[ext] {
		return ext
	}
	return ""
}

// mediaOutput is one file to save: a link to download, or inline data.
type mediaOutput struct {
	link     *url.URL
	declared bool   // the server named it as an output
	data     []byte // inline media
	mimeType string
}

var linkPattern = regexp.MustCompile(`https://[^\s"'<>()\[\]{}\\]+`)

// mediaOutputs lists the result's media files. Resource links and inline media
// are the server's own outputs. Without resource links, a tool that changes
// something (not one that only reads, such as a search or a job list) has its
// text and structured content searched for https links to media files. A link
// the request itself carried is an input, never an output.
func mediaOutputs(res toolResult, arguments map[string]any, search bool) []mediaOutput {
	inputs := map[string]bool{}
	collectStrings(arguments, func(s string) { inputs[s] = true })
	var out []mediaOutput
	seen := map[string]bool{}
	addLink := func(raw string, declared bool, mimeType string) {
		raw = strings.TrimRight(raw, ".,;:!?")
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || seen[u.String()] || inputs[raw] {
			return
		}
		if !declared && mediaExtension(u) == "" {
			return
		}
		if mimeType != "" && !isMediaType(mimeType) && mediaExtension(u) == "" {
			return
		}
		seen[u.String()] = true
		out = append(out, mediaOutput{link: u, declared: declared, mimeType: mimeType})
	}
	linked := false
	for _, c := range res.Content {
		switch c.Type {
		case "resource_link":
			linked = true
			addLink(c.URI, true, c.MimeType)
		case "image", "audio", "video":
			if c.Data == "" || (c.MimeType != "" && !isMediaType(c.MimeType)) {
				continue
			}
			data, err := base64.StdEncoding.DecodeString(c.Data)
			if err == nil && len(data) > 0 {
				out = append(out, mediaOutput{data: data, mimeType: c.MimeType})
			}
		}
	}
	if !linked && search {
		// Structured content is the result itself; the text often repeats it
		// whole, the request's input included, so it is read only without it.
		var structured any
		if json.Unmarshal(res.Structured, &structured) == nil && structured != nil {
			collectOutputStrings(structured, 0, func(s string) {
				for _, m := range linkPattern.FindAllString(s, -1) {
					addLink(m, false, "")
				}
			})
		} else {
			for _, m := range linkPattern.FindAllString(res.text(), -1) {
				addLink(m, false, "")
			}
		}
	}
	if len(out) > maxOutputs {
		out = out[:maxOutputs]
	}
	return out
}

func collectStrings(v any, f func(string)) {
	switch x := v.(type) {
	case string:
		f(x)
	case map[string]any:
		for _, child := range x {
			collectStrings(child, f)
		}
	case []any:
		for _, child := range x {
			collectStrings(child, f)
		}
	}
}

// collectOutputStrings visits the strings of a result, past the parts that
// repeat the request (its input and messages) or report usage.
func collectOutputStrings(v any, depth int, f func(string)) {
	if depth > 12 {
		return
	}
	switch x := v.(type) {
	case string:
		f(x)
	case map[string]any:
		for key, child := range x {
			if key == "input" || key == "messages" || key == "usage" {
				continue
			}
			collectOutputStrings(child, depth+1, f)
		}
	case []any:
		for _, child := range x {
			collectOutputStrings(child, depth+1, f)
		}
	}
}

var reservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true, "CONIN$": true, "CONOUT$": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// safeFileName keeps a name to letters, digits, '.', '-' and '_', so it can
// name no other directory, no hidden file and no device on any system.
func safeFileName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	s := strings.TrimRight(strings.TrimLeft(b.String(), ".-"), ".")
	if strings.Trim(s, "_") == "" {
		return ""
	}
	ext := path.Ext(s)
	if len(ext) > 16 {
		ext = ""
	}
	stem := strings.TrimSuffix(s, ext)
	if len(stem) > 100 {
		stem = stem[:100]
	}
	if stem == "" || reservedNames[strings.ToUpper(strings.SplitN(stem, ".", 2)[0])] {
		stem = "file-" + stem
	}
	return stem + ext
}

// outputName is the file name for an output: the link's own name when it has
// one, else the tool's, with the extension its content type calls for.
func outputName(link *url.URL, contentType, tool string) string {
	name := ""
	if link != nil {
		name = safeFileName(path.Base(link.Path))
	}
	if name == "" {
		name = safeFileName(tool)
	}
	if name == "" {
		name = "output"
	}
	if !mediaExtensions[strings.ToLower(path.Ext(name))] {
		t, _, _ := mime.ParseMediaType(contentType)
		if ext, ok := typeExtensions[t]; ok {
			name += ext
		} else {
			name += ".bin"
		}
	}
	return name
}

// createNew creates name in dir without replacing anything: a name in use
// becomes name-1, name-2 and so on.
func createNew(dir, name string) (*os.File, string, error) {
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 0; i < 10000; i++ {
		candidate := name
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d%s", stem, i, ext)
		}
		target := filepath.Join(dir, candidate)
		if filepath.Dir(target) != filepath.Clean(dir) {
			return nil, "", fmt.Errorf("%q is not a file name", candidate)
		}
		f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			return f, target, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, "", err
		}
	}
	return nil, "", fmt.Errorf("could not find a free name for %s in %s", name, dir)
}

// saveOutput writes one output into dir and returns its path and size. A link
// that does not return a media file is not saved (skipped is true).
func saveOutput(ctx context.Context, o mediaOutput, dir, tool string) (saved string, size int64, skipped bool, err error) {
	if o.link == nil {
		f, target, err := createNew(dir, outputName(nil, o.mimeType, tool))
		if err != nil {
			return "", 0, false, err
		}
		_, err = f.Write(o.data)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(target)
			return "", 0, false, err
		}
		return target, int64(len(o.data)), false, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.link.String(), nil)
	if err != nil {
		return "", 0, false, err
	}
	req.Header.Set("User-Agent", "empirio-cli/"+Version)
	resp, err := downloadClient.Do(req)
	if err != nil {
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err // the cause, without the signed address
		}
		return "", 0, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, false, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	// A link named like a media file is saved unless it returns a web page
	// (3D formats are often served as text or JSON); any other link only
	// when its content type is a media type.
	contentType := resp.Header.Get("Content-Type")
	t, _, _ := mime.ParseMediaType(contentType)
	byName := mediaExtension(resp.Request.URL) != "" || mediaExtension(o.link) != ""
	if !isMediaType(contentType) && !(byName && t != "text/html" && t != "application/xhtml+xml") {
		return "", 0, true, nil
	}
	if resp.ContentLength > maxDownload {
		return "", 0, false, fmt.Errorf("it is larger than %s", sizeLabel(maxDownload))
	}
	f, target, err := createNew(dir, outputName(o.link, contentType, tool))
	if err != nil {
		return "", 0, false, err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, maxDownload+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > maxDownload {
		err = fmt.Errorf("it is larger than %s", sizeLabel(maxDownload))
	}
	if err != nil {
		os.Remove(target)
		return "", 0, false, err
	}
	return target, n, false, nil
}

// sizeLabel is a file size for people.
func sizeLabel(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}
