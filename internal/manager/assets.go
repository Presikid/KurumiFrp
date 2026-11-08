package manager

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
)

type embeddedAsset struct {
	body        []byte
	contentType string
	etag        string
}

type embeddedAssetHandler struct {
	files map[string]embeddedAsset
}

func newEmbeddedAssetHandler(source fs.FS) http.Handler {
	files := make(map[string]embeddedAsset)
	for _, name := range []string{"index.html", "app.js", "core.js", "style.css", "core.css", "kurumi.webp"} {
		body, err := fs.ReadFile(source, name)
		if err != nil {
			continue
		}
		digest := sha256.Sum256(body)
		contentType := mime.TypeByExtension(path.Ext(name))
		if contentType == "" {
			switch path.Ext(name) {
			case ".html":
				contentType = "text/html; charset=utf-8"
			case ".js":
				contentType = "text/javascript; charset=utf-8"
			case ".css":
				contentType = "text/css; charset=utf-8"
			case ".webp":
				contentType = "image/webp"
			}
		}
		files["/"+name] = embeddedAsset{
			body:        body,
			contentType: contentType,
			etag:        "\"" + hex.EncodeToString(digest[:]) + "\"",
		}
	}
	return &embeddedAssetHandler{files: files}
}

func (h *embeddedAssetHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	name := r.URL.Path
	if name == "/" {
		name = "/index.html"
	}
	asset, ok := h.files[name]
	if !ok {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("ETag", asset.etag)
	if asset.contentType != "" {
		w.Header().Set("Content-Type", asset.contentType)
	}
	if matchesStateETag(strings.Join(r.Header.Values("If-None-Match"), ","), asset.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(asset.body)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write(asset.body)
	}
}
