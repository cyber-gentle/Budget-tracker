package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

// staticExts are the file types /static/ serves; everything else in the
// templates folder (page templates, Tailwind sources) stays private.
var staticExts = map[string]bool{
	".css": true, ".js": true, ".svg": true, ".png": true, ".jpg": true, ".jpeg": true,
	".webp": true, ".ico": true, ".woff2": true,
}

// assetStore serves embedded static files with content-hash versioning:
// templates link {{asset "/static/css/tailwind.css"}}, which renders as
// /static/css/tailwind.css?v=<hash>. Versioned URLs are cached for a year,
// and any change to a file changes its URL, so browsers never see stale assets.
type assetStore struct {
	fsys   fs.FS
	hashes map[string]string // "css/tailwind.css" -> short content hash
}

func newAssetStore(fsys fs.FS) (*assetStore, error) {
	a := &assetStore{fsys: fsys, hashes: map[string]string{}}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !staticExts[path.Ext(p)] {
			return err
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		a.hashes[p] = hex.EncodeToString(sum[:])[:12]
		return nil
	})
	return a, err
}

// url returns the versioned URL for a /static/ path, for use in templates.
func (a *assetStore) url(p string) string {
	if h, ok := a.hashes[strings.TrimPrefix(p, "/static/")]; ok {
		return p + "?v=" + h
	}
	return p
}

// ServeHTTP serves /static/<file>. Requests carrying the current ?v= hash get a
// one-year immutable cache; others must revalidate (ETag) on each use.
func (a *assetStore) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/static/")
	hash, ok := a.hashes[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	data, err := fs.ReadFile(a.fsys, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("ETag", `"`+hash+`"`)
	if r.URL.Query().Get("v") == hash {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}
