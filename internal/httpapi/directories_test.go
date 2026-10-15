package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestDirectoryBrowserListsFoldersOnly(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"alpha", "space folder", ".hidden"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("not a folder"), 0600); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	setup(t).Handler().ServeHTTP(w, httptest.NewRequest("GET", "http://localhost/api/v1/directories?path="+url.QueryEscape(root), nil))
	if w.Code != 200 {
		t.Fatalf("got %d: %s", w.Code, w.Body)
	}
	var listing struct {
		Path, Parent string
		Entries      []struct{ Name, Path string }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if listing.Path != canonical || listing.Parent != filepath.Dir(canonical) || len(listing.Entries) != 2 {
		t.Fatalf("unexpected listing: %+v", listing)
	}
	for n, name := range []string{"alpha", "space folder"} {
		if listing.Entries[n].Name != name || listing.Entries[n].Path != filepath.Join(canonical, name) {
			t.Fatalf("unexpected folder: %+v", listing.Entries[n])
		}
	}
}

func TestDirectoryBrowserRejectsInvalidLocationsAndRemoteAccess(t *testing.T) {
	s := setup(t)
	file := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(file, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		path, host, origin string
		status             int
	}{
		{"relative/path", "localhost", "", 422},
		{file, "localhost", "", 422},
		{file + "/missing", "localhost", "", 422},
		{"", "attacker.example", "", 403},
		{"", "localhost", "https://attacker.example", 403},
	} {
		t.Run(item.path+item.host+item.origin, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://localhost/api/v1/directories?path="+url.QueryEscape(item.path), nil)
			r.Host = item.host
			r.Header.Set("Origin", item.origin)
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			if w.Code != item.status {
				t.Fatalf("got %d: %s", w.Code, w.Body)
			}
		})
	}
}
