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
	"strings"
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
	Format       int                         `json:"format"`
	Revision     uint64                      `json:"revision"`
	State        State                       `json:"state"`
	History      []Snapshot                  `json:"history"`
	HistoryLimit int                         `json:"historyLimit"`
	Events       []Event                     `json:"events"`
	Key          string                      `json:"key,omitempty"`
	ActiveCore   *string                     `json:"activeCore"`
	Webhook      *webhookConfig              `json:"webhook,omitempty"`
	Webhooks     []webhookTarget             `json:"webhooks,omitempty"`
	Nodes        *nodeSettings               `json:"nodes,omitempty"`
	Schedules    map[string]instanceSchedule `json:"schedules,omitempty"`
	// Bind local traffic observations to an instance incarnation, not a reused ID.
	LocalTrafficIDs map[string]string `json:"localTrafficIds,omitempty"`
}
type Store struct {
	dir          string
	key          []byte
	DB           Database
	onCommit     func(uint64)
	beforeCommit func(State, State) error
}

// Formats 1 and 2 used a separate master key; format 2 also used snapshot
// files. Format 3 is a single file. Format 4 retains that layout but is an
// explicit compatibility boundary for 0.8 configuration changes.
const currentDatabaseFormat = 4
const maxStateSize = 128 << 20
const maxLegacyStateSize = 64 << 20
const defaultHistoryLimit = 5
const maxHistoryLimit = 100

// Revisions travel as JSON numbers to the browser. Leave half of the exact
// integer range available after an import, including malicious input.
const maxJSONRevision uint64 = 1<<53 - 1
const maxImportedHistoryRevision uint64 = maxJSONRevision / 2

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

// Never silently discard fields in a stored configuration, backup, or typed
// edit. A newer writer must bump the format or an older reader must reject it.
func decodeStrictJSON(data []byte, dst any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return err
	}
	if err := d.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("expected exactly one JSON value")
	}
	return nil
}

// Missing or null TCP/UDP ports used to be invalid. Do not reinterpret them as
// explicit dynamic-allocation requests when loading current or historic data.
// This also accepts an explicit zero written by the port-range development branch.
func requireStoredRemotePorts(data []byte) error {
	type portFields struct {
		Type       string `json:"type"`
		RemotePort *int   `json:"remotePort"`
	}
	type stateFields struct {
		Tunnels []portFields `json:"tunnels"`
	}
	var contents struct {
		State   stateFields `json:"state"`
		History []struct {
			State stateFields `json:"state"`
		} `json:"history"`
	}
	if err := json.Unmarshal(data, &contents); err != nil {
		return err
	}
	check := func(state stateFields) error {
		for _, tunnel := range state.Tunnels {
			if (tunnel.Type == "tcp" || tunnel.Type == "udp") && tunnel.RemotePort == nil {
				return fmt.Errorf("TCP/UDP rule is missing an explicit remotePort")
			}
		}
		return nil
	}
	if err := check(contents.State); err != nil {
		return err
	}
	for _, snapshot := range contents.History {
		if err := check(snapshot.State); err != nil {
			return err
		}
	}
	return nil
}

func singleFileDatabase(data []byte) (Database, error) {
	var db Database
	if err := decodeStrictJSON(data, &db); err != nil {
		return db, err
	}
	if db.Format != 3 && db.Format != currentDatabaseFormat {
		return db, fmt.Errorf("unsupported single-file database format")
	}
	if err := requireStoredRemotePorts(data); err != nil {
		return db, err
	}
	return db, nil
}

// Keep a verified copy of the exact old state before replacing it. Format 1/2
// also depend on master.key and, for format 2, snapshots/: those files are
// deliberately left in place by this version.
func preserveMigrationSource(dir string, format int, data []byte) error {
	f, err := os.CreateTemp(dir, fmt.Sprintf("state.before-format-4-from-%d-*.json.partial", format))
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	matches, err := privateFileMatches(temp, data)
	if err != nil {
		return err
	}
	if !matches {
		return fmt.Errorf("migration source copy did not match")
	}
	dest := strings.TrimSuffix(temp, ".partial")
	if _, err = os.Lstat(dest); err == nil {
		return fmt.Errorf("migration source copy already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err = os.Rename(temp, dest); err != nil {
		return err
	}
	return syncMigrationDir(dir)
}

// Do not initialize an empty database if there are recognizable remnants of
// an earlier migration, even when its state.json or master.key is missing.
func checkMissingState(dir, keyPath string) error {
	for _, path := range []string{keyPath, filepath.Join(dir, "snapshots")} {
		if _, err := os.Lstat(path); err == nil {
			return fmt.Errorf("state missing; existing data retained")
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "state.before-format-4-") {
			return fmt.Errorf("state missing; migration source data retained")
		}
	}
	return nil
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

// Compare without holding an additional full-sized state file in memory.
func privateFileMatches(path string, expected []byte) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("not a regular file: %s", path)
	}
	if info.Size() != int64(len(expected)) {
		return false, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	buf := make([]byte, 32<<10)
	for len(expected) > 0 {
		n := len(buf)
		if n > len(expected) {
			n = len(expected)
		}
		if _, err = io.ReadFull(f, buf[:n]); err != nil {
			return false, err
		}
		if !bytes.Equal(buf[:n], expected[:n]) {
			return false, nil
		}
		expected = expected[n:]
	}
	var extra [1]byte
	n, err := f.Read(extra[:])
	if n == 0 && err == io.EOF {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, nil
}

func incrementRevision(db *Database) error {
	if db.Revision >= maxJSONRevision {
		return fmt.Errorf("配置版本号已达上限，无法安全修改")
	}
	db.Revision++
	return nil
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
	s := &Store{dir: dir}
	dbPath := filepath.Join(dir, "state.json")
	keyPath := filepath.Join(dir, "master.key")
	b, e := privateFileLimit(dbPath, maxStateSize)
	if os.IsNotExist(e) {
		if err := checkMissingState(dir, keyPath); err != nil {
			return nil, err
		}
		s.key = make([]byte, 32)
		if _, e = rand.Read(s.key); e != nil {
			return nil, e
		}
		s.DB = Database{Format: currentDatabaseFormat, Revision: 1, State: State{Instances: []Instance{}, Tunnels: []Tunnel{}}, History: []Snapshot{}, Events: []Event{}, HistoryLimit: defaultHistoryLimit}
		return s, s.write(s.DB)
	}
	if e != nil {
		return nil, e
	}
	var header struct {
		Format          int             `json:"format"`
		LocalTrafficIDs json.RawMessage `json:"localTrafficIds"`
	}
	if e = json.Unmarshal(b, &header); e != nil {
		return nil, e
	}
	switch header.Format {
	case 1:
		if e = decodeStrictJSON(b, &s.DB); e != nil {
			return nil, fmt.Errorf("cannot read format 1 state; original retained: %w", e)
		}
		if e = requireStoredRemotePorts(b); e != nil {
			return nil, fmt.Errorf("cannot read format 1 state; original retained: %w", e)
		}
		s.key, e = privateFile(keyPath)
		if e != nil || len(s.key) != 32 {
			return nil, fmt.Errorf("invalid or missing encryption key; original retained")
		}
		if s.DB.Key != "" {
			embeddedKey, err := base64.RawStdEncoding.DecodeString(s.DB.Key)
			if err != nil || !bytes.Equal(embeddedKey, s.key) {
				return nil, fmt.Errorf("conflicting legacy encryption keys; original retained")
			}
		}
		if len(s.DB.History) > 30 {
			return nil, fmt.Errorf("unsupported database")
		}
		var options struct {
			HistoryLimit json.RawMessage `json:"historyLimit"`
		}
		if e = json.Unmarshal(b, &options); e != nil {
			return nil, e
		}
		if len(options.HistoryLimit) != 0 {
			if bytes.Equal(bytes.TrimSpace(options.HistoryLimit), []byte("null")) || s.DB.HistoryLimit < len(s.DB.History) || s.DB.HistoryLimit > maxHistoryLimit {
				return nil, fmt.Errorf("invalid legacy history limit; original retained")
			}
		} else {
			// Format 1 originally had no limit; retain all existing snapshots.
			s.DB.HistoryLimit = defaultHistoryLimit
			if len(s.DB.History) > s.DB.HistoryLimit {
				s.DB.HistoryLimit = len(s.DB.History)
			}
		}
	case 2:
		var disk diskState
		if e = decodeStrictJSON(b, &disk); e != nil {
			return nil, fmt.Errorf("cannot read format 2 state; original retained: %w", e)
		}
		if e = requireStoredRemotePorts(b); e != nil {
			return nil, fmt.Errorf("cannot read format 2 state; original retained: %w", e)
		}
		s.key, e = privateFile(keyPath)
		if e != nil || len(s.key) != 32 {
			return nil, fmt.Errorf("invalid or missing encryption key; original retained")
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
		refs := map[uint64]bool{}
		for _, ref := range disk.History {
			if !snapshotName.MatchString(ref.File) || ref.Revision == 0 || ref.Revision >= disk.Revision || refs[ref.Revision] {
				return nil, fmt.Errorf("invalid snapshot reference")
			}
			data, err := privateFileLimit(filepath.Join(dir, "snapshots", ref.File), maxLegacyStateSize)
			if err != nil {
				return nil, err
			}
			var snap Snapshot
			if err = decodeStrictJSON(data, &snap); err != nil {
				return nil, fmt.Errorf("cannot read legacy snapshot; original retained: %w", err)
			}
			if err = requireStoredRemotePorts(data); err != nil {
				return nil, fmt.Errorf("cannot read legacy snapshot; original retained: %w", err)
			}
			if snap.Revision != ref.Revision {
				return nil, fmt.Errorf("snapshot revision mismatch")
			}
			refs[ref.Revision] = true
			s.DB.History = append(s.DB.History, snap)
		}
	case 3, currentDatabaseFormat:
		s.DB, e = singleFileDatabase(b)
		if e != nil {
			return nil, fmt.Errorf("cannot read configuration; original retained: %w", e)
		}
		s.key, e = base64.RawStdEncoding.DecodeString(s.DB.Key)
		if s.DB.HistoryLimit < 0 || s.DB.HistoryLimit > maxHistoryLimit || len(s.DB.History) > s.DB.HistoryLimit {
			return nil, fmt.Errorf("invalid history limit")
		}
	default:
		return nil, fmt.Errorf("unsupported database format %d; original retained", header.Format)
	}
	if e != nil || len(s.key) != 32 {
		return nil, fmt.Errorf("invalid or missing encryption key; original retained")
	}
	if e = validateStoredWebhook(s, s.DB.Webhook); e != nil {
		return nil, fmt.Errorf("invalid local webhook settings; original retained")
	}
	if e = validateStoredWebhookTargets(s, s.DB.Webhook, s.DB.Webhooks); e != nil {
		return nil, fmt.Errorf("invalid local webhook targets; original retained")
	}
	if e = validateStoredNodes(s, s.DB.Nodes); e != nil {
		return nil, fmt.Errorf("invalid local node settings; original retained")
	}
	if s.DB.Revision < 1 || s.DB.Revision > maxJSONRevision {
		return nil, fmt.Errorf("unsupported database revision; original retained")
	}
	for _, snapshot := range s.DB.History {
		if snapshot.Revision > maxJSONRevision {
			return nil, fmt.Errorf("unsupported snapshot revision; original retained")
		}
	}
	if e = validate(s.DB.State); e != nil {
		return nil, e
	}
	if len(header.LocalTrafficIDs) == 0 {
		// Older state files have no local identity mapping. Persist one before
		// loading history so an interrupted upgrade cannot attach old data.
		s.DB.LocalTrafficIDs = freshLocalTrafficIDs(s.DB.State)
	} else if e = validateLocalTrafficIDs(s.DB.State, s.DB.LocalTrafficIDs); e != nil {
		return nil, fmt.Errorf("invalid local traffic identities; original retained: %w", e)
	}
	if e = validateSchedules(s.DB.State, s.DB.Schedules); e != nil {
		return nil, fmt.Errorf("invalid schedules; original retained: %w", e)
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
			if e = s.validateStoredHTTPHeaders(t); e != nil {
				return nil, e
			}
			if t.Secret != "" {
				if _, e = s.decrypt(t.Secret); e != nil {
					return nil, e
				}
			}
			if t.HTTPPassword != "" {
				if _, e = s.decrypt(t.HTTPPassword); e != nil {
					return nil, e
				}
			}
		}
	}
	if header.Format != currentDatabaseFormat {
		if e = preserveMigrationSource(dir, header.Format, b); e != nil {
			return nil, fmt.Errorf("cannot preserve original configuration before migration: %w", e)
		}
		matches, err := privateFileMatches(dbPath, b)
		if err != nil || !matches {
			return nil, fmt.Errorf("configuration changed during migration; original copy retained")
		}
		if e = s.write(s.DB); e != nil {
			return nil, fmt.Errorf("cannot write migrated configuration; original copy retained: %w", e)
		}
		s.DB.Format = currentDatabaseFormat
	} else if len(header.LocalTrafficIDs) == 0 && len(s.DB.State.Instances) != 0 {
		matches, err := privateFileMatches(dbPath, b)
		if err != nil || !matches {
			return nil, fmt.Errorf("configuration changed while adding traffic identities; original retained")
		}
		if e = s.write(s.DB); e != nil {
			return nil, fmt.Errorf("cannot save traffic identities; original retained: %w", e)
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
	return s.encryptValue(plain)
}

// encryptValue seals already-validated UTF-8 data, including an empty value.
func (s *Store) encryptValue(plain string) (string, error) {
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
	db.Format = currentDatabaseFormat
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
	return nil
}
func (s *Store) commit(st State, summary string) error {
	return s.commitRestoring(st, summary, false)
}

func (s *Store) commitRestoring(st State, summary string, restoring bool) error {
	if e := validate(st); e != nil {
		return e
	}
	if e := validateVisitorListeners(s.DB.State, st); e != nil {
		return e
	}
	if s.beforeCommit != nil {
		if e := s.beforeCommit(s.DB.State, st); e != nil {
			return e
		}
	}
	db := s.DB
	db.Schedules = schedulesAfterStateChange(db.Schedules, db.State, st, restoring)
	db.History = append(append([]Snapshot{}, db.History...), Snapshot{db.Revision, now(), summary, db.State.clone()})
	if len(db.History) > db.HistoryLimit {
		db.History = db.History[len(db.History)-db.HistoryLimit:]
	}
	if e := incrementRevision(&db); e != nil {
		return e
	}
	db.State = st
	if restoring {
		db.LocalTrafficIDs = freshLocalTrafficIDs(st)
	} else {
		db.LocalTrafficIDs = nextLocalTrafficIDs(s.DB.State, st, s.DB.LocalTrafficIDs)
	}
	db.Events = append(append([]Event{}, db.Events...), Event{now(), summary})
	if len(db.Events) > 200 {
		db.Events = db.Events[len(db.Events)-200:]
	}
	if e := s.write(db); e != nil {
		return e
	}
	s.DB = db
	s.DB.Format = currentDatabaseFormat
	if s.onCommit != nil {
		s.onCommit(db.Revision)
	}
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
	if e := incrementRevision(&db); e != nil {
		return e
	}
	db.Events = append(append([]Event{}, db.Events...), Event{now(), fmt.Sprintf("快照保留数量设置为 %d", limit)})
	if len(db.Events) > 200 {
		db.Events = db.Events[len(db.Events)-200:]
	}
	if e := s.write(db); e != nil {
		return e
	}
	db.Format = currentDatabaseFormat
	s.DB = db
	return nil
}
