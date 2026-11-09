//go:build !linux

package manager

import (
	"os"
	"testing"
)

func TestStatusRecoveryPreservesUnverifiableOwnership(t *testing.T) {
	root := t.TempDir()
	s, err := newPrivateStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.cleanup()
	s.markStarted(2147483647)
	reapPrivateStatuses(root)
	if _, err := os.Stat(s.dir); err != nil {
		t.Fatal("unverifiable directory was removed", err)
	}
}
