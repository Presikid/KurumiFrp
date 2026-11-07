package manager

import (
	"bytes"
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
	"regexp"
	"runtime"
	"time"
	"unicode/utf8"
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
	Format       int        `json:"format"`
	Revision     uint64     `json:"revision"`
	State        State      `json:"state"`
	History      []Snapshot `json:"history"`
	HistoryLimit int        `json:"historyLimit"`
	Events       []Event    `json:"events"`
	Key          string     `json:"key,omitempty"`
	ActiveCore   *string    `json:"activeCore"`
}
type Store struct {
	dir  string
	key  []byte
	DB   Database
	refs map[uint64]string
}

const maxStateSize = 128 << 20
const maxLegacyStateSize = 64 << 20
const defaultHistoryLimit = 5
const maxHistoryLimit = 100

type snapshotRef struct {
	Revision uint64 `json:"revision"`
	File     string `json:"file"`
}

type diskState struct {
	Format       int           `json:"format"`
	Revision     uint64        `json:"revision"`
	State        State         `json:"state"`
	Events       []Event       `json:"events"`
	HistoryLimit int           `json:"historyLimit"`
	History      []snapshotRef `json:"history"`
}

var snapshotName = regexp.MustCompile(`^snapshot-[a-f0-9]{32}\.json$`)

func randomHex(n int) string {
	b := make([]byte, n)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func now() string { return time.Now().UTC().Format(time.RFC3339) }
func privateFile(path string) ([]byte, error) {
	return privateFileLimit(path, maxStateSize)
}
func privateFileLimit(path string, limit int64) ([]byte, error) {
	f, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	if !f.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", path)
	}
	if f.Size() > limit {
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

// writeIfChanged avoids replacing an unchanged private regular file. Changed
// data still uses fsync + atomic rename: a crash must not truncate state.json.
// Callers serialize writes and create the parent directory themselves.
func writeIfChanged(path string, data []byte) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if err == nil {
		if !info.Mode().IsRegular() {
			return false, fmt.Errorf("not a regular file: %s", path)
		}
		private := runtime.GOOS == "windows" || info.Mode().Perm() == 0600
		if private && info.Size() == int64(len(data)) {
			existing, readErr := privateFileLimit(path, int64(len(data)))
			if readErr != nil {
				return false, readErr
			}
			if bytes.Equal(existing, data) {
				return false, nil
			}
		}
	}
	if err := atomicWrite(path, data); err != nil {
		return false, err
	}
	return true, nil
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
	s := &Store{dir: dir, refs: map[uint64]string{}}
	dbPath := filepath.Join(dir, "state.json")
	keyPath := filepath.Join(dir, "master.key")
	b, e := privateFileLimit(dbPath, maxStateSize)
	if os.IsNotExist(e) {
		if _, err := os.Lstat(keyPath); err == nil {
			return nil, fmt.Errorf("state missing; existing master key retained")
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		s.key = make([]byte, 32)
		if _, e = rand.Read(s.key); e != nil {
			return nil, e
		}
		s.DB = Database{Format: 3, Revision: 1, State: State{Instances: []Instance{}, Tunnels: []Tunnel{}}, History: []Snapshot{}, Events: []Event{}, HistoryLimit: defaultHistoryLimit}
		return s, s.write(s.DB)
	}
	if e != nil {
		return nil, e
	}
	var header struct {
		Format int `json:"format"`
	}
	if e = json.Unmarshal(b, &header); e != nil {
		return nil, e
	}
	if header.Format == 3 {
		if e = json.Unmarshal(b, &s.DB); e != nil {
			return nil, e
		}
		s.key, e = base64.RawStdEncoding.DecodeString(s.DB.Key)
	} else {
		s.key, e = privateFile(keyPath)
	}
	if e != nil || len(s.key) != 32 {
		return nil, fmt.Errorf("invalid or missing encryption key")
	}
	switch header.Format {
	case 1:
		if e = json.Unmarshal(b, &s.DB); e != nil {
			return nil, e
		}
		if len(s.DB.History) > 30 {
			return nil, fmt.Errorf("unsupported database")
		}
		// Preserve all existing snapshots until the user changes the limit.
		s.DB.HistoryLimit = defaultHistoryLimit
		if len(s.DB.History) > s.DB.HistoryLimit {
			s.DB.HistoryLimit = len(s.DB.History)
		}
	case 2:
		var disk diskState
		if e = json.Unmarshal(b, &disk); e != nil {
			return nil, e
		}
		if disk.HistoryLimit < 0 || disk.HistoryLimit > maxHistoryLimit || len(disk.History) > disk.HistoryLimit {
			return nil, fmt.Errorf("invalid history limit")
		}
		if len(disk.History) > 0 {
			if e = snapshotDir(dir, false); e != nil {
				return nil, e
			}
		}
		s.DB = Database{Format: 2, Revision: disk.Revision, State: disk.State, Events: disk.Events, History: []Snapshot{}, HistoryLimit: disk.HistoryLimit}
		for _, ref := range disk.History {
			if !snapshotName.MatchString(ref.File) || ref.Revision == 0 || ref.Revision >= disk.Revision || s.refs[ref.Revision] != "" {
				return nil, fmt.Errorf("invalid snapshot reference")
			}
			data, err := privateFileLimit(filepath.Join(dir, "snapshots", ref.File), maxLegacyStateSize)
			if err != nil {
				return nil, err
			}
			var snap Snapshot
			if err = json.Unmarshal(data, &snap); err != nil {
				return nil, err
			}
			if snap.Revision != ref.Revision {
				return nil, fmt.Errorf("snapshot revision mismatch")
			}
			s.refs[ref.Revision] = ref.File
			s.DB.History = append(s.DB.History, snap)
		}
	case 3:
		if s.DB.HistoryLimit < 0 || s.DB.HistoryLimit > maxHistoryLimit || len(s.DB.History) > s.DB.HistoryLimit {
			return nil, fmt.Errorf("invalid history limit")
		}
	default:
		return nil, fmt.Errorf("unsupported database")
	}
	if s.DB.Revision < 1 {
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
	if header.Format != 3 {
		if e = s.write(s.DB); e != nil {
			return nil, e
		}
		s.DB.Format = 3
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
	if !utf8.ValidString(plain) {
		return "", fmt.Errorf("密钥必须是有效的 UTF-8 文本")
	}
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
	if !utf8.Valid(p) {
		return "", fmt.Errorf("decrypted secret is not valid UTF-8")
	}
	return string(p), nil
}
func snapshotDir(dir string, create bool) error {
	p := filepath.Join(dir, "snapshots")
	if create {
		if e := os.Mkdir(p, 0700); e != nil && !os.IsExist(e) {
			return e
		}
	}
	st, e := os.Lstat(p)
	if e != nil {
		return e
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("unsafe snapshot directory")
	}
	return nil
}

// The complete configuration and its history are committed in one atomic file.
func (s *Store) write(db Database) error {
	db.Format = 3
	db.Key = base64.RawStdEncoding.EncodeToString(s.key)
	b, err := json.MarshalIndent(db, "", "  ")
	if err != nil {
		return err
	}
	if len(b) > maxStateSize {
		return fmt.Errorf("配置文件超过 128 MiB，无法安全保存；请减少快照数量")
	}
	if _, err = writeIfChanged(filepath.Join(s.dir, "state.json"), b); err != nil {
		return err
	}
	if s.DB.Format != 3 {
		for _, name := range s.refs {
			_ = os.Remove(filepath.Join(s.dir, "snapshots", name))
		}
		if len(s.refs) > 0 {
			_ = os.Remove(filepath.Join(s.dir, "snapshots"))
		}
		_ = os.Remove(filepath.Join(s.dir, "master.key"))
		s.refs = map[uint64]string{}
	}
	return nil
}
func (s *Store) commit(st State, summary string) error {
	if e := validate(st); e != nil {
		return e
	}
	if e := validateVisitorListeners(s.DB.State, st); e != nil {
		return e
	}
	db := s.DB
	db.History = append(append([]Snapshot{}, db.History...), Snapshot{db.Revision, now(), summary, db.State.clone()})
	if len(db.History) > db.HistoryLimit {
		db.History = db.History[len(db.History)-db.HistoryLimit:]
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
	s.DB.Format = 3
	return nil
}

func (s *Store) setHistoryLimit(limit int) error {
	if limit < 0 || limit > maxHistoryLimit {
		return fmt.Errorf("快照数量须为 0-100")
	}
	db := s.DB
	db.HistoryLimit = limit
	db.History = append([]Snapshot{}, db.History...)
	if len(db.History) > limit {
		db.History = db.History[len(db.History)-limit:]
	}
	db.Revision++
	db.Events = append(append([]Event{}, db.Events...), Event{now(), fmt.Sprintf("快照保留数量设置为 %d", limit)})
	if len(db.Events) > 200 {
		db.Events = db.Events[len(db.Events)-200:]
	}
	if e := s.write(db); e != nil {
		return e
	}
	db.Format = 3
	s.DB = db
	return nil
}
