package api

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pegnia/sidecar/internal/config"
)

// newTestServer serves a data root at <tmp>/data next to a sibling <tmp>/data-other
// directory, the classic target of a prefix-check bypass.
func newTestServer(t *testing.T, apiKey string) (http.Handler, string, string) {
	t.Helper()
	base := t.TempDir()
	dataRoot := filepath.Join(base, "data")
	other := filepath.Join(base, "data-other")
	for _, d := range []string{dataRoot, other, filepath.Join(dataRoot, "world")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(dataRoot, "server.properties"), "motd=hi")
	write(t, filepath.Join(other, "secret.txt"), "secret")

	s, err := NewServer(&config.Config{
		API:  config.APIConfig{APIKey: apiKey, RateLimit: 1000},
		Data: config.DataConfig{Root: dataRoot},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.root.Close() })
	return s.Handler(), dataRoot, other
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func do(h http.Handler, method, target string, body io.Reader, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, body)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestSanitizePath(t *testing.T) {
	ok := map[string]string{
		"":              ".",
		"/":             ".",
		".":             ".",
		"mods":          "mods",
		"/mods":         "mods",
		"world/../mods": "mods",
		"a//b/":         filepath.Join("a", "b"),
	}
	for in, want := range ok {
		got, err := sanitizePath(in)
		if err != nil || got != filepath.FromSlash(want) {
			t.Errorf("sanitizePath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"..", "../data-other/x", "/../mods", "../../etc/passwd", "/../../etc/passwd", "a/../../b", "a\x00b"} {
		if got, err := sanitizePath(in); err == nil {
			t.Errorf("sanitizePath(%q) = %q; want it rejected", in, got)
		}
	}
}

func TestTraversalCannotReachSiblingDirectory(t *testing.T) {
	h, _, _ := newTestServer(t, "")
	for _, p := range []string{"../data-other/secret.txt", "/../data-other/secret.txt", "..%2Fdata-other%2Fsecret.txt"} {
		rec := do(h, "GET", "/api/files/download?path="+p, nil, nil)
		if rec.Code == http.StatusOK || strings.Contains(rec.Body.String(), "secret") {
			t.Errorf("download %q: status %d body %q; must not reach the sibling directory", p, rec.Code, rec.Body.String())
		}
	}
}

func TestSymlinkOutOfRootIsRefused(t *testing.T) {
	h, dataRoot, other := newTestServer(t, "")
	if err := os.Symlink(other, filepath.Join(dataRoot, "escape")); err != nil {
		t.Fatal(err)
	}
	rec := do(h, "GET", "/api/files/download?path=escape/secret.txt", nil, nil)
	if rec.Code == http.StatusOK || strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("download through symlink: status %d body %q", rec.Code, rec.Body.String())
	}
	rec = do(h, "GET", "/api/files?path=escape", nil, nil)
	if rec.Code == http.StatusOK {
		t.Fatalf("list through symlink: status %d body %q", rec.Code, rec.Body.String())
	}
	// Deleting the link removes the link only.
	rec = do(h, "POST", "/api/files/delete", strings.NewReader(`{"path":"escape"}`), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete symlink: status %d body %q", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(other, "secret.txt")); err != nil {
		t.Fatalf("symlink target was touched: %v", err)
	}
}

func TestFileOperations(t *testing.T) {
	h, dataRoot, _ := newTestServer(t, "")

	rec := do(h, "GET", "/api/files?path=/", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	var files []FileInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &files); err != nil || len(files) != 2 {
		t.Fatalf("list = %v (%v)", files, err)
	}

	rec = do(h, "GET", "/api/files/download?path=/server.properties", nil, nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "motd=hi" {
		t.Fatalf("download: %d %q", rec.Code, rec.Body)
	}
	if rec = do(h, "GET", "/api/files/download?path=missing", nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("download missing: %d", rec.Code)
	}

	rec = do(h, "POST", "/api/files/create-dir", strings.NewReader(`{"path":"/plugins/config"}`), nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create-dir: %d %s", rec.Code, rec.Body)
	}
	if info, err := os.Stat(filepath.Join(dataRoot, "plugins", "config")); err != nil || !info.IsDir() {
		t.Fatalf("directory not created: %v", err)
	}

	upload := func(name, overwrite string) *httptest.ResponseRecorder {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		fw, _ := mw.CreateFormFile("file", name)
		fw.Write([]byte("jar"))
		mw.Close()
		return do(h, "POST", "/api/files/upload?path=plugins"+overwrite, &body,
			map[string]string{"Content-Type": mw.FormDataContentType()})
	}
	if rec = upload("mod.jar", ""); rec.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}
	if rec = upload("mod.jar", ""); rec.Code != http.StatusConflict {
		t.Fatalf("upload existing: %d", rec.Code)
	}
	if rec = upload("mod.jar", "&overwrite=true"); rec.Code != http.StatusCreated {
		t.Fatalf("upload overwrite: %d %s", rec.Code, rec.Body)
	}
	if rec = upload("run.sh", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("upload .sh: %d", rec.Code)
	}

	rec = do(h, "POST", "/api/files/delete", strings.NewReader(`{"path":"/plugins"}`), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(filepath.Join(dataRoot, "plugins")); !os.IsNotExist(err) {
		t.Fatalf("plugins not deleted: %v", err)
	}
	for _, p := range []string{"/", "", ".", "../"} {
		rec = do(h, "POST", "/api/files/delete", strings.NewReader(`{"path":"`+p+`"}`), nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("delete root %q: %d", p, rec.Code)
		}
	}
}

func TestAPIKey(t *testing.T) {
	h, _, _ := newTestServer(t, "s3cret")
	if rec := do(h, "GET", "/health", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("health must not need a key: %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/files", nil, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no key: %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/files", nil, map[string]string{"X-API-Key": "wrong"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key: %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/files", nil, map[string]string{"X-API-Key": "s3cret"}); rec.Code != http.StatusOK {
		t.Fatalf("right key: %d", rec.Code)
	}
}
