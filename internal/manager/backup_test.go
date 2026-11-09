package manager

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
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
	got, err = pbkdf2SHA256([]byte("password"), []byte("salt"), 2, 32)
	if err != nil {
		t.Fatal(err)
	}
	want, _ = hex.DecodeString("ae4d0c95af6b46d32d0adff928f06dd02a303f8ef3c251dfd6e2d85a95474c43")
	if string(got) != string(want) {
		t.Fatalf("PBKDF2 iteration mismatch %x", got)
	}
}

type notifyingReader struct {
	reader  io.Reader
	started chan struct{}
	once    sync.Once
}

func (n *notifyingReader) Read(p []byte) (int, error) {
	n.once.Do(func() { close(n.started) })
	return n.reader.Read(p)
}

func TestBackupSlowUploadDoesNotBlockState(t *testing.T) {
	a := testApp(t)
	seed(t, a)
	revision := a.store.DB.Revision
	reader, writer := io.Pipe()
	defer writer.Close()
	started := make(chan struct{})
	r := httptest.NewRequest("POST", "http://127.0.0.1:9000/api/backup/import", &notifyingReader{reader: reader, started: started})
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+a.AccessToken())
	done := make(chan int, 1)
	go func() { w := httptest.NewRecorder(); a.ServeHTTP(w, r); done <- w.Code }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("upload handler did not start")
	}
	state := make(chan int, 1)
	go func() { state <- request(a, "GET", "/api/state", nil, a.AccessToken(), "").Code }()
	select {
	case code := <-state:
		if code != http.StatusOK {
			t.Fatalf("state returned %d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("slow backup upload blocked state")
	}
	if w := request(a, "POST", "/api/backup/export", map[string]any{"revision": a.store.DB.Revision, "password": "correct-horse-battery-staple"}, a.AccessToken(), ""); w.Code != http.StatusConflict {
		t.Fatalf("concurrent backup returned %d", w.Code)
	}
	instance := a.store.DB.State.public().Instances[0]
	instance.Name = "changed while uploading"
	if w := request(a, "POST", "/api/save", map[string]any{"revision": revision, "kind": "instance", "value": instance}, a.AccessToken(), ""); w.Code != http.StatusOK {
		t.Fatalf("save during upload returned %d: %s", w.Code, w.Body.String())
	}
	if err := json.NewEncoder(writer).Encode(map[string]any{"revision": revision, "password": "correct-horse-battery-staple", "backup": EncryptedBackup{}}); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	select {
	case code := <-done:
		if code != http.StatusConflict {
			t.Fatalf("stale restore returned %d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("upload handler did not stop")
	}
	if a.store.DB.State.Instances[0].Name != instance.Name {
		t.Fatal("stale backup changed state")
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
