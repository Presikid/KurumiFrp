package manager

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestEmbeddedHandlerServesNewNestedAsset(t *testing.T) {
	assets := fstest.MapFS{
		"index.html":        &fstest.MapFile{Data: []byte("home")},
		"nested/feature.js": &fstest.MapFile{Data: []byte("feature")},
	}
	handler := newEmbeddedAssetHandler(assets)
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/nested/feature.js", nil))
	if first.Code != http.StatusOK || first.Body.String() != "feature" {
		t.Fatalf("additional asset response: %d %q", first.Code, first.Body.String())
	}
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("additional asset has no ETag")
	}
	request := httptest.NewRequest(http.MethodGet, "/nested/feature.js", nil)
	request.Header.Set("If-None-Match", etag)
	cached := httptest.NewRecorder()
	handler.ServeHTTP(cached, request)
	if cached.Code != http.StatusNotModified || cached.Body.Len() != 0 {
		t.Fatalf("conditional asset response: %d %q", cached.Code, cached.Body.String())
	}
}

func TestAppRoutesEmbeddedNestedAssets(t *testing.T) {
	a := testApp(t)
	a.assets = newEmbeddedAssetHandler(fstest.MapFS{"nested/feature.js": &fstest.MapFile{Data: []byte("feature")}})
	w := request(a, http.MethodGet, "/nested/feature.js", nil, "", "")
	if w.Code != http.StatusOK || w.Body.String() != "feature" {
		t.Fatalf("app asset response: %d %q", w.Code, w.Body.String())
	}
	w = request(a, http.MethodGet, "/nested/missing.js", nil, "", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing asset response: %d", w.Code)
	}
}
