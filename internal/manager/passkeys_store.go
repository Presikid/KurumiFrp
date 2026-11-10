package manager

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

const (
	passkeyRPID = "localhost"
	maxPasskeys = 1000
	maxPasskeysPerAccount = 10
	maxPasskeysFile = 4 << 20
)

// This file contains public credential records, never authenticator private
// keys. It is separate from portable configuration and owned by auth.mu.
type passkeyRecord struct {
	AccountID string              `json:"accountId"`
	Name      string              `json:"name"`
	RPID      string              `json:"rpId"`
	CreatedAt string              `json:"createdAt"`
	Flags     uint8               `json:"flags"`
	Credential webauthn.Credential `json:"credential"`
}

type passkeyFile struct {
	Version  int             `json:"version"`
	Revision uint64          `json:"revision"`
	Records  []passkeyRecord `json:"records"`
}

type passkeyStore struct {
	path string
	data passkeyFile
	loadErr error
}

func validPasskeyRecords(records []passkeyRecord) bool {
	if len(records) > maxPasskeys {
		return false
	}
	ids := make(map[string]bool, len(records))
	counts := make(map[string]int)
	for _, record := range records {
		c := record.Credential
		flags := webauthn.NewCredentialFlags(protocol.AuthenticatorFlags(record.Flags))
		if !accountIDPattern.MatchString(record.AccountID) || !oidcTextOK(record.Name, 80) || record.RPID != passkeyRPID ||
			len(c.ID) == 0 || len(c.ID) > 1024 || len(c.PublicKey) == 0 || len(c.PublicKey) > 8192 || ids[string(c.ID)] ||
			c.Flags.UserPresent != flags.UserPresent || c.Flags.UserVerified != flags.UserVerified || c.Flags.BackupEligible != flags.BackupEligible || c.Flags.BackupState != flags.BackupState {
			return false
		}
		if _, err := time.Parse(time.RFC3339, record.CreatedAt); err != nil {
			return false
		}
		encoded, err := json.Marshal(c)
		if err != nil || len(encoded) > 128<<10 {
			return false
		}
		ids[string(c.ID)] = true
		counts[record.AccountID]++
		if counts[record.AccountID] > maxPasskeysPerAccount {
			return false
		}
	}
	return true
}

func loadPasskeys(dir string) *passkeyStore {
	s := &passkeyStore{path: filepath.Join(dir, "passkeys.json"), data: passkeyFile{Version: 1, Revision: 1, Records: []passkeyRecord{}}}
	content, err := privateFileLimit(s.path, maxPasskeysFile)
	if os.IsNotExist(err) {
		return s
	}
	if err != nil {
		s.loadErr = err
		return s
	}
	var file passkeyFile
	if decodeStrictJSON(content, &file) != nil || file.Version != 1 || file.Revision == 0 || file.Revision > maxJSONRevision || !validPasskeyRecords(file.Records) {
		s.loadErr = fmt.Errorf("Passkey 文件无效，原文件已保留")
		return s
	}
	for i := range file.Records {
		file.Records[i].Credential.Flags = webauthn.NewCredentialFlags(protocol.AuthenticatorFlags(file.Records[i].Flags))
	}
	s.data = file
	return s
}

// Caller holds auth.mu. Publish the exact replacement only after its atomic
// write; a directory-sync failure leaves the new in-memory state in effect.
func (s *passkeyStore) save(records []passkeyRecord) error {
	if s.loadErr != nil || s.data.Revision >= maxJSONRevision || !validPasskeyRecords(records) {
		return fmt.Errorf("无法保存 Passkey 配置")
	}
	next := passkeyFile{Version: 1, Revision: s.data.Revision + 1, Records: append([]passkeyRecord{}, records...)}
	content, err := json.Marshal(next)
	if err != nil || len(content) > maxPasskeysFile {
		return fmt.Errorf("Passkey 配置过大或无效")
	}
	if _, err = writeIfChanged(s.path, content); err != nil {
		return fmt.Errorf("无法保存 Passkey 配置")
	}
	s.data = next
	if syncMigrationDir(filepath.Dir(s.path)) != nil {
		return fmt.Errorf("Passkey 已写入但持久化未确认，请重新核对")
	}
	return nil
}
