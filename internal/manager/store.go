package manager

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

type Snapshot struct {
	Revision uint64 `json:"revision"`
	At       string `json:"at"`
	Summary  string `json:"summary"`
	State    State  `json:"state"`
}
type Event struct {
	At      string `json:"at"`
	Message string `json:"message"`
}
type Database struct {
	Format   int        `json:"format"`
	Revision uint64     `json:"revision"`
	State    State      `json:"state"`
	History  []Snapshot `json:"history"`
	Events   []Event    `json:"events"`
}
type Store struct {
	dir string
	key []byte
	DB  Database
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func now() string { return time.Now().UTC().Format(time.RFC3339) }
func privateFile(path string) ([]byte, error) {
	f, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	if !f.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", path)
	}
	if f.Size() > 8<<20 {
		return nil, fmt.Errorf("file too large")
	}
	return os.ReadFile(path)
}
func atomicWrite(path string, b []byte) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".kurumi-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}
func newStore(dir string) (*Store, error) {
	dir, e := filepath.Abs(dir)
	if e != nil {
		return nil, e
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	if st, e := os.Lstat(dir); e != nil || st.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("unsafe data directory")
	}
	s := &Store{dir: dir}
	dbPath := filepath.Join(dir, "state.json")
	keyPath := filepath.Join(dir, "master.key")
	s.key, e = privateFile(keyPath)
	if os.IsNotExist(e) {
		if _, de := os.Stat(dbPath); !os.IsNotExist(de) {
			return nil, fmt.Errorf("database exists but encryption key is missing; restore key, do not regenerate")
		}
		s.key = make([]byte, 32)
		if _, e = rand.Read(s.key); e != nil {
			return nil, e
		}
		e = atomicWrite(keyPath, s.key)
	}
	if e != nil {
		return nil, e
	}
	if len(s.key) != 32 {
		return nil, fmt.Errorf("invalid encryption key")
	}
	b, e := privateFile(dbPath)
	if os.IsNotExist(e) {
		s.DB = Database{Format: 1, Revision: 1, State: State{Instances: []Instance{}, Tunnels: []Tunnel{}}, History: []Snapshot{}, Events: []Event{}}
		return s, s.write(s.DB)
	}
	if e != nil {
		return nil, e
	}
	if e = json.Unmarshal(b, &s.DB); e != nil {
		return nil, e
	}
	if s.DB.Format != 1 || s.DB.Revision < 1 || len(s.DB.History) > 30 {
		return nil, fmt.Errorf("unsupported database")
	}
	if e = validate(s.DB.State); e != nil {
		return nil, e
	}
	for _, st := range append([]State{s.DB.State}, historyStates(s.DB.History)...) {
		if e = validate(st); e != nil {
			return nil, e
		}
		for _, i := range st.Instances {
			if _, e = s.decrypt(i.Token); e != nil {
				return nil, e
			}
		}
		for _, t := range st.Tunnels {
			if t.Secret != "" {
				if _, e = s.decrypt(t.Secret); e != nil {
					return nil, e
				}
			}
		}
	}
	return s, nil
}
func historyStates(h []Snapshot) []State {
	out := []State{}
	for _, s := range h {
		out = append(out, s.State)
	}
	return out
}
func (s *Store) encrypt(plain string) (string, error) {
	if len(plain) < 8 || len(plain) > 1024 {
		return "", fmt.Errorf("令牌/配对密钥长度须为 8-1024 字节")
	}
	for _, c := range plain {
		if c < 32 || c == 127 {
			return "", fmt.Errorf("密钥不能包含控制字符")
		}
	}
	block, e := aes.NewCipher(s.key)
	if e != nil {
		return "", e
	}
	g, e := cipher.NewGCM(block)
	if e != nil {
		return "", e
	}
	n := make([]byte, g.NonceSize())
	if _, e = io.ReadFull(rand.Reader, n); e != nil {
		return "", e
	}
	return base64.RawStdEncoding.EncodeToString(g.Seal(n, n, []byte(plain), []byte("kurumi-v1"))), nil
}
func (s *Store) decrypt(text string) (string, error) {
	b, e := base64.RawStdEncoding.DecodeString(text)
	if e != nil {
		return "", fmt.Errorf("invalid encrypted secret")
	}
	block, e := aes.NewCipher(s.key)
	if e != nil {
		return "", e
	}
	g, e := cipher.NewGCM(block)
	if e != nil {
		return "", e
	}
	if len(b) < g.NonceSize() {
		return "", fmt.Errorf("invalid encrypted secret")
	}
	p, e := g.Open(nil, b[:g.NonceSize()], b[g.NonceSize():], []byte("kurumi-v1"))
	if e != nil {
		return "", fmt.Errorf("secret decryption failed")
	}
	return string(p), nil
}
func (s *Store) write(db Database) error {
	b, e := json.MarshalIndent(db, "", "  ")
	if e != nil {
		return e
	}
	return atomicWrite(filepath.Join(s.dir, "state.json"), b)
}
func (s *Store) commit(st State, summary string) error {
	if e := validate(st); e != nil {
		return e
	}
	db := s.DB
	db.History = append(append([]Snapshot{}, db.History...), Snapshot{db.Revision, now(), summary, db.State.clone()})
	if len(db.History) > 30 {
		db.History = db.History[len(db.History)-30:]
	}
	db.Revision++
	db.State = st
	db.Events = append(append([]Event{}, db.Events...), Event{now(), summary})
	if len(db.Events) > 200 {
		db.Events = db.Events[len(db.Events)-200:]
	}
	if e := s.write(db); e != nil {
		return e
	}
	s.DB = db
	return nil
}
