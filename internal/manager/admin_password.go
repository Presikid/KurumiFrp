package manager

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"
)

const adminPasswordIterations = 600000

func (a *auth) acquireHash() bool {
	select {
	case a.hashSlots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (a *auth) releaseHash() { <-a.hashSlots }

func passwordHash(password string, salt []byte) ([]byte, error) {
	return pbkdf2SHA256([]byte(password), salt, adminPasswordIterations, 32)
}

// The administrator password is independent of account files and the local
// maintenance token. A missing password file initializes an empty password.
type adminPasswordFile struct {
	Version int    `json:"version"`
	Salt    string `json:"salt"`
	Hash    string `json:"hash"`
}

func newAdminPassword(password string) (adminPasswordFile, error) {
	if !utf8.ValidString(password) || len(password) > 1024 {
		return adminPasswordFile{}, fmt.Errorf("密码须为不超过 1024 字节的有效文本")
	}
	for _, c := range []byte(password) {
		if c < 32 || c == 127 {
			return adminPasswordFile{}, fmt.Errorf("密码不能包含控制字符")
		}
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return adminPasswordFile{}, err
	}
	hash, err := passwordHash(password, salt)
	if err != nil {
		return adminPasswordFile{}, err
	}
	return adminPasswordFile{Version: 1, Salt: base64.RawStdEncoding.EncodeToString(salt), Hash: base64.RawStdEncoding.EncodeToString(hash)}, nil
}

func readAdminPassword(path string) (adminPasswordFile, []byte, error) {
	data, err := privateFileLimit(path, 4096)
	if err != nil {
		return adminPasswordFile{}, nil, err
	}
	var file adminPasswordFile
	if err = decodeStrictJSON(data, &file); err != nil || file.Version != 1 {
		return adminPasswordFile{}, nil, fmt.Errorf("主密码文件无效，原文件已保留")
	}
	salt, saltErr := base64.RawStdEncoding.DecodeString(file.Salt)
	hash, hashErr := base64.RawStdEncoding.DecodeString(file.Hash)
	if saltErr != nil || len(salt) != 16 || hashErr != nil || len(hash) != 32 {
		return adminPasswordFile{}, nil, fmt.Errorf("主密码文件无效，原文件已保留")
	}
	return file, data, nil
}

// Create without replacing any existing path. The preferred hard-link commit
// exposes only a complete synced file. Filesystems without hard links use an
// exclusive create; an interrupted write is retained and rejected on restart.
// The bool says whether the destination may have been created even on error.
func createAdminPasswordFile(path string, data []byte) (bool, error) {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".admin-password-init-*")
	if err != nil {
		return false, err
	}
	name := f.Name()
	defer os.Remove(name)
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
		return false, err
	}
	if err = os.Link(name, path); err == nil {
		return true, syncMigrationDir(dir)
	} else if os.IsExist(err) {
		return false, err
	}
	f, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return false, err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return true, err
	}
	return true, syncMigrationDir(dir)
}

// Initialize the default before the store starts, while holding the data lock.
func prepareAdminPassword(dir string) (adminPasswordFile, []byte, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return adminPasswordFile{}, nil, err
	}
	if err = os.MkdirAll(abs, 0700); err != nil {
		return adminPasswordFile{}, nil, err
	}
	if st, err := os.Lstat(abs); err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return adminPasswordFile{}, nil, fmt.Errorf("unsafe data directory")
	}
	path := filepath.Join(abs, "admin-password.json")
	file, data, err := readAdminPassword(path)
	if err == nil {
		return file, data, nil
	}
	if !os.IsNotExist(err) {
		return adminPasswordFile{}, nil, err
	}
	file, err = newAdminPassword("")
	if err != nil {
		return adminPasswordFile{}, nil, err
	}
	data, err = json.MarshalIndent(file, "", "  ")
	if err != nil {
		return adminPasswordFile{}, nil, err
	}
	_, err = createAdminPasswordFile(path, data)
	if os.IsExist(err) {
		file, data, err = readAdminPassword(path)
	}
	if err != nil {
		return adminPasswordFile{}, nil, fmt.Errorf("无法初始化主密码，已有文件已保留: %w", err)
	}
	return file, data, nil
}

func verifyAdminPassword(file adminPasswordFile, password string) (bool, error) {
	salt, err := base64.RawStdEncoding.DecodeString(file.Salt)
	if err != nil {
		return false, err
	}
	want, err := base64.RawStdEncoding.DecodeString(file.Hash)
	if err != nil {
		return false, err
	}
	got, err := passwordHash(password, salt)
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// The bool reports a credential failure rather than a capacity or state error.
func (a *auth) loginAdminPassword(r *http.Request, password string) (string, bool, error) {
	if len(password) > 1024 {
		return "", true, fmt.Errorf("密码不正确")
	}
	a.mu.Lock()
	file, generation, loadErr := a.adminPassword, a.adminPasswordGeneration, a.adminPasswordErr
	a.mu.Unlock()
	if loadErr != nil {
		return "", false, fmt.Errorf("主密码文件无法安全确认，请重启并核对原文件")
	}
	if !a.acquireHash() {
		return "", false, fmt.Errorf("登录繁忙，请稍后再试")
	}
	valid, err := verifyAdminPassword(file, password)
	a.releaseHash()
	if err != nil || !valid {
		return "", true, fmt.Errorf("密码不正确")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.adminPasswordErr != nil || a.adminPasswordGeneration != generation {
		return "", false, fmt.Errorf("密码状态已变化，请重试")
	}
	sid, err := a.newSessionLocked(r, time.Now())
	return sid, false, err
}

func (a *App) adminPasswordAPI(w http.ResponseWriter, r *http.Request, p principal) {
	var req struct {
		Old *string `json:"old"`
		New *string `json:"new"`
	}
	if err := decode(w, r, &req); err != nil || req.Old == nil || req.New == nil {
		fail(w, http.StatusBadRequest, fmt.Errorf("请提供原密码和新密码"))
		return
	}
	const key = "admin-password"
	if err := a.auth.beginAttempt(key); err != nil {
		fail(w, http.StatusTooManyRequests, err)
		return
	}
	failed := false
	defer func() { a.auth.finishAttempt(key, failed) }()
	if len(*req.Old) > 1024 {
		failed = true
		fail(w, http.StatusBadRequest, fmt.Errorf("原密码不正确"))
		return
	}
	a.auth.mu.Lock()
	original, generation, loadErr := a.auth.adminPassword, a.auth.adminPasswordGeneration, a.auth.adminPasswordErr
	a.auth.mu.Unlock()
	if loadErr != nil {
		fail(w, http.StatusConflict, fmt.Errorf("主密码文件无法安全确认，请重启并核对原文件"))
		return
	}
	if !a.auth.acquireHash() {
		fail(w, http.StatusTooManyRequests, fmt.Errorf("密码操作繁忙，请稍后再试"))
		return
	}
	valid, err := verifyAdminPassword(original, *req.Old)
	if err != nil || !valid {
		a.auth.releaseHash()
		failed = true
		fail(w, http.StatusBadRequest, fmt.Errorf("原密码不正确"))
		return
	}
	newFile, err := newAdminPassword(*req.New)
	a.auth.releaseHash()
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	data, err := json.MarshalIndent(newFile, "", "  ")
	if err != nil {
		fail(w, http.StatusInternalServerError, fmt.Errorf("无法保存主密码"))
		return
	}
	// Do not wait behind an admitted long-running request.
	if !a.auth.rootGate.TryLock() {
		fail(w, http.StatusTooManyRequests, fmt.Errorf("其他操作正在进行，本次密码修改未提交，请稍后重试"))
		return
	}
	defer a.auth.rootGate.Unlock()
	a.auth.mu.Lock()
	defer a.auth.mu.Unlock()
	current, ok := a.auth.identityLocked(r)
	if !ok || current.AccountID != "" || current.gate != p.gate {
		fail(w, http.StatusUnauthorized, fmt.Errorf("请先登录"))
		return
	}
	if a.auth.adminPasswordErr != nil || a.auth.adminPasswordGeneration != generation {
		fail(w, http.StatusConflict, fmt.Errorf("密码状态已变化，请重新登录"))
		return
	}
	path := a.auth.adminPasswordPath
	committed := false
	matches, err := privateFileMatches(path, a.auth.adminPasswordData)
	credentialUncertain := err != nil || !matches
	if err == nil && !matches {
		err = fmt.Errorf("主密码文件已变化，原文件已保留")
	}
	if err == nil {
		_, err = writeIfChanged(path, data)
		committed = err == nil
		if committed {
			err = syncMigrationDir(filepath.Dir(path))
		}
	}
	if committed || credentialUncertain {
		// A credential conflict invalidates cached authentication. A file that
		// may have been committed must be read back before it can be trusted.
		if committed {
			stored, storedData, readErr := readAdminPassword(path)
			if readErr == nil && stored != newFile {
				readErr = fmt.Errorf("主密码文件在保存时发生变化")
			}
			a.auth.adminPasswordErr = readErr
			if readErr == nil {
				a.auth.adminPassword, a.auth.adminPasswordData = stored, storedData
			}
		} else {
			a.auth.adminPasswordErr = fmt.Errorf("主密码文件状态无法安全确认")
		}
		a.auth.adminPasswordGeneration++
		for sid := range a.auth.sessions {
			delete(a.auth.sessions, sid)
		}
	}
	if a.auth.adminPasswordErr != nil {
		fail(w, http.StatusConflict, fmt.Errorf("主密码文件无法安全确认，请重启并核对原文件"))
		return
	}
	if err != nil {
		fail(w, http.StatusConflict, fmt.Errorf("主密码无法安全保存或确认；已有文件已保留，请重新登录"))
		return
	}
	reply(w, http.StatusOK, map[string]bool{"ok": true})
}
