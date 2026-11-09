package version

import (
	"os"
	"strings"
	"testing"
)

func TestVersionMatchesReleaseFile(t *testing.T) {
	b, err := os.ReadFile("../../VERSION")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(b)); got != Current {
		t.Fatalf("VERSION = %q, application = %q", got, Current)
	}
}
