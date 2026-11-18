package manager

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWebReleaseCheckAndApply(t *testing.T) {
	const tag = "v99.0.0"
	var latestCalls, manifestCalls, packageCalls atomic.Int32
	slug, ext, _ := frpPlatformSlug()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, repo := range []releaseRepo{appRepo, frpRepo} {
			base := "/" + repo.name + "/releases"
			download := base + "/download/" + tag + "/"
			switch r.URL.Path {
			case base + "/latest":
				latestCalls.Add(1)
				http.Redirect(w, r, base+"/tag/"+tag, http.StatusFound)
				return
			case base + "/tag/" + tag:
				w.WriteHeader(http.StatusOK)
				return
			case download + repo.checksumFile:
				manifestCalls.Add(1)
				names := []string{"KurumiFRP-99.0.0-Setup.exe", "KurumiFRP-99.0.0-linux-amd64.tar.gz", "KurumiFRP-99.0.0-linux-arm64.tar.gz"}
				if repo == frpRepo {
					names = []string{"frp_99.0.0_" + slug + ext}
				}
				for _, name := range names {
					fmt.Fprintf(w, "%x  %s\n", sha256.Sum256([]byte("published package")), name)
				}
				return
			}
			if strings.HasPrefix(r.URL.Path, download) {
				packageCalls.Add(1)
				fmt.Fprint(w, "wrong package")
				return
			}
		}
		t.Errorf("unexpected outbound request: %s %s", r.Method, r.URL)
		http.NotFound(w, r)
	}))
	defer server.Close()

	u := NewUpdateManager(t.TempDir(), nil, nil)
	u.webBase, u.allowLocal = server.URL, true
	// Route even external HTTPS connections through this fixture. Unexpected
	// API requests fail the test without contacting GitHub.
	proxy := server.URL
	check, err := u.CheckAll(context.Background(), &proxy)
	if err != nil || check.App.Error != "" || check.FRP.Error != "" || check.App.Latest != "99.0.0" || check.FRP.Latest != "99.0.0" {
		t.Fatalf("check: %+v, %v", check, err)
	}
	var applied int32
	if check.App.CanAutoUpdate {
		_, err := u.ApplyAppUpdate(context.Background(), nil, "", nil)
		if err == nil || !strings.Contains(err.Error(), "SHA-256 校验未通过") {
			t.Fatalf("app apply: %v", err)
		}
		applied++
	}
	if check.FRP.CanAutoUpdate {
		_, _, err := u.ApplyFRPUpdate(context.Background(), nil)
		if err == nil || !strings.Contains(err.Error(), "SHA-256 校验不一致") {
			t.Fatalf("FRP apply: %v", err)
		}
		applied++
	}
	if latestCalls.Load() != 2 || manifestCalls.Load() != 2 || packageCalls.Load() != applied {
		t.Fatalf("lookups %d, manifests %d, packages %d", latestCalls.Load(), manifestCalls.Load(), packageCalls.Load())
	}
}

func TestWebReleaseFailureAndRetry(t *testing.T) {
	const base = "/Presikid/KurumiFrp/releases"
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case base + "/latest":
			if attempts.Add(1) == 1 {
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			http.Redirect(w, r, base+"/tag/v1.0.0", http.StatusFound)
		case base + "/tag/v1.0.0":
			w.WriteHeader(http.StatusOK)
		case base + "/download/v1.0.0/SHA256SUMS.txt":
			fmt.Fprintf(w, "%x  KurumiFRP-1.0.0-Setup.exe\n", sha256.Sum256([]byte("installer")))
		default:
			t.Errorf("unexpected outbound request: %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	u := NewUpdateManager(t.TempDir(), nil, nil)
	u.webBase, u.allowLocal = server.URL, true
	client, err := u.httpClient(server.URL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, err = u.latestRelease(context.Background(), client, server.URL, appRepo)
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("first lookup: %v", err)
	}
	rel, err := u.latestRelease(context.Background(), client, server.URL, appRepo)
	if err != nil || rel.TagName != "v1.0.0" || attempts.Load() != 2 {
		t.Fatalf("retry: %+v, %v", rel, err)
	}
	// A release download redirect must not switch to a REST API endpoint.
	api, _ := url.Parse("https://api.github.com/repos/Presikid/KurumiFrp/releases/latest")
	if err := validateDownloadURL(api, false); err == nil {
		t.Fatal("redirect to REST API was accepted")
	}
}
