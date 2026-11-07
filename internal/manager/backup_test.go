package manager

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func TestBackupPBKDF2Vector(t *testing.T) {
	got, err := pbkdf2SHA256([]byte("password"), []byte("salt"), 1, 32)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := hex.DecodeString("120fb6cffcf8b32c43e7225256c4f837a86548c92ccc35480805987cb70be17b")
	if string(got) != string(want) {
		t.Fatalf("PBKDF2 mismatch %x", got)
	}
}

func TestEncryptedBackupRoundTrip(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	db := Database{Format: 3, Revision: 9}
	password := "correct-horse-battery-staple"
	backup, err := encryptBackup(db, key, password)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := decryptBackup(backup, password)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Revision != db.Revision || restored.Key != base64.RawStdEncoding.EncodeToString(key) {
		t.Fatal("bad backup")
	}
	if _, err = decryptBackup(backup, "wrong-password-123"); err == nil {
		t.Fatal("wrong password accepted")
	}
	altered := backup
	if altered.Ciphertext[0] == 'A' {
		altered.Ciphertext = "B" + altered.Ciphertext[1:]
	} else {
		altered.Ciphertext = "A" + altered.Ciphertext[1:]
	}
	if _, err = decryptBackup(altered, password); err == nil {
		t.Fatal("tamper accepted")
	}
	bad := backup
	bad.Iterations = maxBackupIterations + 1
	if _, err = decryptBackup(bad, password); err == nil {
		t.Fatal("invalid iteration count accepted")
	}
	if _, err = encryptBackup(db, key, "short"); err == nil {
		t.Fatal("weak password accepted")
	}
}

func TestEncryptedBackupAPI(t *testing.T) {
	a := testApp(t)
	instance := seed(t, a)
	password := "correct-horse-battery-staple"
	w := request(a, "POST", "/api/backup/export", map[string]any{"revision": a.store.DB.Revision, "password": password}, a.AccessToken(), "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "test-secret-1234") {
		t.Fatal("plaintext leaked")
	}
	var backup EncryptedBackup
	if err := json.Unmarshal(w.Body.Bytes(), &backup); err != nil {
		t.Fatal(err)
	}
	rev := a.store.DB.Revision
	w = request(a, "POST", "/api/backup/import", map[string]any{"revision": rev, "password": "wrong-password-123", "backup": backup}, a.AccessToken(), "")
	if w.Code != 400 || a.store.DB.Revision != rev {
		t.Fatal("bad password modified state")
	}
	pub := a.store.DB.State.public().Instances[0]
	pub.Name = "Changed after backup"
	w = request(a, "POST", "/api/save", map[string]any{"revision": rev, "kind": "instance", "value": pub}, a.AccessToken(), "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = request(a, "POST", "/api/backup/import", map[string]any{"revision": rev, "password": password, "backup": backup}, a.AccessToken(), "")
	if w.Code != 409 {
		t.Fatal("stale restore accepted")
	}
	current := a.store.DB.Revision
	w = request(a, "POST", "/api/backup/import", map[string]any{"revision": current, "password": password, "backup": backup}, a.AccessToken(), "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if a.store.DB.State.Instances[0].Name != instance.Name {
		t.Fatal("instance not restored")
	}
	decrypted, err := a.store.decrypt(a.store.DB.State.Instances[0].Token)
	if err != nil || decrypted != "test-secret-1234" {
		t.Fatal("key not restored", err)
	}
	if a.store.DB.Revision <= current {
		t.Fatal("revision did not advance")
	}
}
