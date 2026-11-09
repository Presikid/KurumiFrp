package update

import (
	"context"
	"os"
	"testing"
	"time"
)

// Opt-in because ordinary unit tests must not depend on external availability.
func TestLiveOfficialHTMLFallbackDownload(t *testing.T) {
	if os.Getenv("KURUMI_LIVE_RELEASE_TEST") != "1" {
		t.Skip("live GitHub release test is disabled")
	}
	m := New(t.TempDir(), "windows-installed")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	r, err := m.htmlReleaseAsset(ctx, "v0.7.1.14")
	if err != nil {
		t.Fatal(err)
	}
	a, ok := assetFor(r, "windows-installed")
	if !ok {
		t.Fatal("official page did not provide a verifiable Windows asset")
	}
	if _, err := m.download(ctx, a, t.TempDir()); err != nil {
		t.Fatal(err)
	}
}
