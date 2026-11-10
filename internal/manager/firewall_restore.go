package manager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

func firewallRestoreDigest(config firewallConfig) (string, error) {
	data, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func (f *firewallManager) restoreMarkerPath() string {
	return filepath.Join(filepath.Dir(f.path), "firewall-applied.hash")
}

// The marker is published only after nft confirms this exact saved revision.
// A later saved configuration cannot inherit an older revision's marker.
func (f *firewallManager) recordRestoreStatusLocked() {
	if !f.config.Enabled || !f.config.AutoRestore {
		f.restoreStatus = "未启用启动时恢复"
		return
	}
	digest, err := firewallRestoreDigest(f.config)
	if err == nil {
		_, err = writeIfChanged(f.restoreMarkerPath(), []byte(digest))
	}
	if err == nil {
		err = syncMigrationDir(filepath.Dir(f.path))
	}
	if err != nil {
		f.restoreStatus = "规则已应用，但启动恢复记录未确认；重启后需核对"
		return
	}
	f.restoreStatus = "规则已应用，启动时恢复已启用"
}

// Restore only an explicitly enabled configuration whose exact content was
// confirmed by a previous apply. Consume the marker before applying so an
// interrupted or failed attempt cannot automatically repeat on the next boot.
func (a *App) restoreFirewall(ctx context.Context) {
	f := a.firewall
	if f == nil || !firewallSupported() {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.loadError || f.last != "unknown" || !f.config.Enabled || !f.config.AutoRestore {
		return
	}
	f.restoreStatus = "启动恢复未确认；请核对并手动应用"
	digest, err := firewallRestoreDigest(f.config)
	if err != nil {
		return
	}
	marker, err := privateFileLimit(f.restoreMarkerPath(), 128)
	if err != nil || string(marker) != digest {
		return
	}
	rules, err := renderFirewall(f.config)
	if err != nil {
		return
	}
	select {
	case <-a.quit:
		return
	default:
	}
	if ctx.Err() != nil {
		return
	}
	if err = os.Remove(f.restoreMarkerPath()); err != nil {
		return
	}
	if err = syncMigrationDir(filepath.Dir(f.path)); err != nil {
		return
	}
	f.last = "unknown"
	f.refreshStatus = "启动恢复期间自动刷新暂停"
	if err = applyFirewall(ctx, rules); err != nil {
		f.restoreStatus = "启动恢复的内核结果未确认；自动恢复和刷新已暂停，请核对"
		return
	}
	f.last = "applied"
	f.resetRefreshLocked(time.Now())
	f.recordRestoreStatusLocked()
}
