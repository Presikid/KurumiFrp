package manager

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Accounts are deliberately separate from configuration exports and snapshots.
// The local access token remains a full administrator and recovery credential.
const accountIterations = 600000
const maxAccounts = 100

var accountNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{2,31}$`)
var accountIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

type localAccount struct {
	gate            *sync.RWMutex `json:"-"`
	ID              string        `json:"id"`
	Username        string        `json:"username"`
	Role            string        `json:"role"`
	Enabled         bool          `json:"enabled"`
	ExpiresAt       string        `json:"expiresAt,omitempty"`
	Instances       []string      `json:"instances"`
	ControlInstances *[]string     `json:"controlInstances,omitempty"`
	AllowedActions  *[]string     `json:"allowedActions,omitempty"`
	ReadPermissions *accountReadPermissions `json:"readPermissions,omitempty"`
	AllowLogs       bool          `json:"allowLogs,omitempty"`
	LogInstances    *[]string     `json:"logInstances,omitempty"`
	RemoteInstances []remoteGrant `json:"remoteInstances,omitempty"`
	Salt            string        `json:"salt"`
	Hash            string        `json:"hash"`
}

type remoteGrant struct {
	NodeID     string `json:"nodeId"`
	InstanceID string `json:"instanceId"`
	Binding    string `json:"binding"`
	Control    bool   `json:"control,omitempty"`
}

type accountReadPermissions struct {
	Config  bool `json:"config"`
	Traffic bool `json:"traffic"`
}

type accountFile struct {
	Version  int            `json:"version"`
	Revision uint64         `json:"revision"`
	Accounts []localAccount `json:"accounts"`
}

type accountView struct {
	ID               string   `json:"id"`
	Username         string   `json:"username"`
	Role             string   `json:"role"`
	Enabled          bool     `json:"enabled"`
	ExpiresAt        string   `json:"expiresAt"`
	Expired          bool     `json:"expired"`
	Instances        []string `json:"instances"`
	ControlInstances []string `json:"controlInstances"`
	AllowedActions   []string `json:"allowedActions"`
	ReadPermissions  accountReadPermissions `json:"readPermissions"`
	AllowLogs        bool     `json:"allowLogs"`
	LogInstances     []string `json:"logInstances"`
	RemoteInstances  []remoteGrant `json:"remoteInstances"`
}

type principalContextKey struct{}

func requestPrincipal(r *http.Request) principal {
	p, _ := r.Context().Value(principalContextKey{}).(principal)
	return p
}

func withPrincipal(r *http.Request, p principal) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), principalContextKey{}, p))
}

func loadAccounts(dir string) ([]localAccount, uint64, error) {
	data, err := privateFileLimit(filepath.Join(dir, "accounts.json"), 1<<20)
	if os.IsNotExist(err) {
		return []localAccount{}, 1, nil
	}
	if err != nil {
		return nil, 0, err
	}
	var file accountFile
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&file); err != nil {
		return nil, 0, fmt.Errorf("本地账户文件无效")
	}
	if err := d.Decode(&struct{}{}); err != io.EOF || file.Version != 1 || file.Revision == 0 || file.Revision > maxJSONRevision || len(file.Accounts) > maxAccounts {
		return nil, 0, fmt.Errorf("本地账户文件无效")
	}
	names, ids := map[string]bool{}, map[string]bool{}
	for i, u := range file.Accounts {
		if normalized, err := normalizeAccountExpiry(u.ExpiresAt); err != nil || normalized != u.ExpiresAt {
			return nil, 0, fmt.Errorf("本地账户文件中的到期时间无效；原文件已保留")
		}
		if !validRemoteGrants(u.RemoteInstances) || !validRemoteGrantRole(u.Role, u.RemoteInstances) {
			return nil, 0, fmt.Errorf("本地账户文件中的远程实例授权无效；原文件已保留")
		}
		salt, saltErr := base64.RawStdEncoding.DecodeString(u.Salt)
		hash, hashErr := base64.RawStdEncoding.DecodeString(u.Hash)
		name := strings.ToLower(u.Username)
		if !accountIDPattern.MatchString(u.ID) || !accountNamePattern.MatchString(u.Username) || !validRole(u.Role) || names[name] || ids[u.ID] || saltErr != nil || len(salt) != 16 || hashErr != nil || len(hash) != 32 || !validGrants(u.Instances) || (u.Role == "admin" && len(u.Instances) != 0) || (u.ControlInstances != nil && (!validGrants(*u.ControlInstances) || !subsetGrants(*u.ControlInstances, u.Instances) || (u.Role != "operator" && len(*u.ControlInstances) != 0))) {
			return nil, 0, fmt.Errorf("本地账户文件无效")
		}
		if u.LogInstances != nil && (!validGrants(*u.LogInstances) || !subsetGrants(*u.LogInstances, u.Instances) || ((u.Role == "admin" || !u.AllowLogs) && len(*u.LogInstances) != 0)) {
			return nil, 0, fmt.Errorf("本地账户文件中的日志授权无效")
		}
		if u.AllowedActions != nil && (!validOperatorActions(*u.AllowedActions) || (u.Role != "operator" && len(*u.AllowedActions) != 0)) {
			return nil, 0, fmt.Errorf("本地账户文件中的操作授权无效；原文件已保留")
		}
		if u.Role == "admin" && u.ReadPermissions != nil {
			return nil, 0, fmt.Errorf("管理员账户的查看权限配置无效；原文件已保留")
		}
		names[name], ids[u.ID] = true, true
		file.Accounts[i].gate = &sync.RWMutex{}
	}
	return file.Accounts, file.Revision, nil
}

func validRole(role string) bool { return role == "admin" || role == "operator" || role == "viewer" }

func validOperatorActions(actions []string) bool {
	if len(actions) > 4 {
		return false
	}
	seen := make(map[string]bool, len(actions))
	for _, action := range actions {
		if (action != "start" && action != "stop" && action != "restart" && action != "validate") || seen[action] {
			return false
		}
		seen[action] = true
	}
	return true
}

// A missing field preserves existing operators' previous permissions. An
// explicit empty list grants no actions, even for otherwise visible instances.
func effectiveOperatorActions(user localAccount) []string {
	if user.Role != "operator" {
		return []string{}
	}
	if user.AllowedActions == nil {
		return []string{"start", "stop", "restart", "validate"}
	}
	return append([]string{}, (*user.AllowedActions)...)
}

// Accounts created before separate read permissions retain their prior view.
func effectiveReadPermissions(user localAccount) accountReadPermissions {
	if user.Role == "admin" || user.ReadPermissions == nil {
		return accountReadPermissions{Config: true, Traffic: true}
	}
	return *user.ReadPermissions
}

// Deadlines are stored in UTC with whole-second precision. Empty means no
// expiration; an invalid persisted value must never grant access.
func normalizeAccountExpiry(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if len(value) > 40 {
		return "", fmt.Errorf("账户到期时间无效")
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil || t.Nanosecond() != 0 || t.UTC().Year() < 1970 || t.UTC().Year() > 9999 {
		return "", fmt.Errorf("账户到期时间须为带时区的时间，精确到秒")
	}
	return t.UTC().Format(time.RFC3339), nil
}

func (u localAccount) activeAt(now time.Time) bool {
	if !u.Enabled {
		return false
	}
	if u.ExpiresAt == "" {
		return true
	}
	expires, err := time.Parse(time.RFC3339, u.ExpiresAt)
	return err == nil && now.Before(expires)
}

func validGrants(ids []string) bool {
	if len(ids) > 1000 {
		return false
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !idPattern.MatchString(id) || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

func validRemoteGrants(grants []remoteGrant) bool {
	if len(grants) > 1000 {
		return false
	}
	seen := make(map[string]bool, len(grants))
	for _, grant := range grants {
		key := grant.NodeID + ":" + grant.InstanceID
		if !idPattern.MatchString(grant.NodeID) || !idPattern.MatchString(grant.InstanceID) || !nodeTokenPattern.MatchString(grant.Binding) || seen[key] {
			return false
		}
		seen[key] = true
	}
	return true
}

func validRemoteGrantRole(role string, grants []remoteGrant) bool {
	if role == "admin" {
		return len(grants) == 0
	}
	for _, grant := range grants {
		if grant.Control && role != "operator" {
			return false
		}
	}
	return true
}

func subsetGrants(selected, allowed []string) bool {
	grants := make(map[string]bool, len(allowed))
	for _, id := range allowed {
		grants[id] = true
	}
	for _, id := range selected {
		if !grants[id] {
			return false
		}
	}
	return true
}

func retainGrants(selected, allowed []string) []string {
	retained := make([]string, 0, len(selected))
	for _, id := range selected {
		for _, candidate := range allowed {
			if id == candidate {
				retained = append(retained, id)
				break
			}
		}
	}
	return retained
}

// Missing controlInstances preserves the previous operator behavior.
func effectiveControlInstances(user localAccount) []string {
	if user.Role != "operator" {
		return []string{}
	}
	if user.ControlInstances == nil {
		return append([]string{}, user.Instances...)
	}
	return append([]string{}, (*user.ControlInstances)...)
}

// Missing logInstances retains the previous all-visible-instances grant.
func effectiveLogInstances(user localAccount) []string {
	if user.Role == "admin" || !user.AllowLogs {
		return []string{}
	}
	if user.LogInstances == nil {
		return append([]string{}, user.Instances...)
	}
	return append([]string{}, (*user.LogInstances)...)
}

// Caller holds auth.mu. Publishing in-memory state only follows a successful write.
func (a *auth) writeAccounts(accounts []localAccount) error {
	if a.accountsRevision >= maxJSONRevision {
		return fmt.Errorf("账户版本已达到上限")
	}
	data, err := json.MarshalIndent(accountFile{Version: 1, Revision: a.accountsRevision + 1, Accounts: accounts}, "", "  ")
	if err != nil {
		return err
	}
	if len(data) > 1<<20 {
		return fmt.Errorf("账户配置过大")
	}
	if _, err = writeIfChanged(a.accountPath, data); err != nil {
		return err
	}
	a.accounts = accounts
	a.accountsRevision++
	return nil
}

func (a *auth) revoke(id string) {
	for sid, session := range a.sessions {
		if session.accountID == id {
			delete(a.sessions, sid)
		}
	}
}

// Called with App.mu held. Revoking before a delete or replacement is
// fail-closed even if the subsequent configuration write fails. Restoring or
// importing a different configuration must not revive old instance grants.
func (a *App) clearInstanceGrantsLocked(instanceID string) error {
	a.auth.mu.Lock()
	defer a.auth.mu.Unlock()
	users := append([]localAccount(nil), a.auth.accounts...)
	changed := false
	for i, user := range users {
		if user.Role == "admin" || len(user.Instances) == 0 {
			continue
		}
		grants := make([]string, 0, len(user.Instances))
		for _, id := range user.Instances {
			if instanceID == "" || id == instanceID {
				changed = true
			} else {
				grants = append(grants, id)
			}
		}
		users[i].Instances = grants
		if user.ControlInstances != nil {
			controls := retainGrants(*user.ControlInstances, grants)
			users[i].ControlInstances = &controls
		}
		if user.LogInstances != nil {
			logs := retainGrants(*user.LogInstances, grants)
			users[i].LogInstances = &logs
		}
	}
	if !changed {
		return nil
	}
	return a.auth.writeAccounts(users)
}

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
	return pbkdf2SHA256([]byte(password), salt, accountIterations, 32)
}

func newAccountHash(password string) (string, string, error) {
	if err := validateBackupPassword(password); err != nil {
		return "", "", fmt.Errorf("密码须为 12 至 1024 字节的有效文本，且不能包含控制字符")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", "", err
	}
	hash, err := passwordHash(password, salt)
	if err != nil {
		return "", "", err
	}
	return base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash), nil
}

// The boolean reports a credential failure, not a capacity or state error.
func (a *auth) loginAccount(r *http.Request, username, password string) (string, bool, error) {
	if len(username) > 32 || len(password) > 1024 {
		// These inputs cannot match the supported account credentials.
		return "", false, fmt.Errorf("用户名或密码不正确")
	}
	a.mu.Lock()
	var account localAccount
	for _, u := range a.accounts {
		if strings.EqualFold(u.Username, username) {
			account = u
			break
		}
	}
	revision := a.accountsRevision
	a.mu.Unlock()
	if !a.acquireHash() {
		return "", false, fmt.Errorf("登录繁忙，请稍后再试")
	}
	hashHeld := true
	defer func() {
		if hashHeld {
			a.releaseHash()
		}
	}()
	// Unknown names do the same derivation and produce the same error.
	salt := make([]byte, 16)
	want := make([]byte, 32)
	if account.ID != "" {
		salt, _ = base64.RawStdEncoding.DecodeString(account.Salt)
		want, _ = base64.RawStdEncoding.DecodeString(account.Hash)
	}
	got, err := passwordHash(password, salt)
	a.releaseHash()
	hashHeld = false
	if err != nil {
		return "", false, fmt.Errorf("用户名或密码不正确")
	}
	if subtle.ConstantTimeCompare(got, want) != 1 || !account.activeAt(time.Now()) {
		return "", true, fmt.Errorf("用户名或密码不正确")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	// A disable/re-enable or other change during password derivation must not
	// revive an authentication attempt that began before revocation.
	if a.accountsRevision != revision {
		return "", false, fmt.Errorf("账户状态已变化，请重试")
	}
	for _, current := range a.accounts {
		if current.ID == account.ID && current.activeAt(time.Now()) && current.Salt == account.Salt && current.Hash == account.Hash {
			sid, err := a.newSessionLocked(r, current.ID, time.Now())
			return sid, false, err
		}
	}
	return "", false, fmt.Errorf("用户名或密码不正确")
}

func (a *App) accountsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		a.auth.mu.Lock()
		if a.auth.accountLoadErr != nil {
			a.auth.mu.Unlock()
			fail(w, 503, fmt.Errorf("账户文件无法读取，原文件已保留；请停止程序、备份并移走该文件后重启"))
			return
		}
		views := make([]accountView, 0, len(a.auth.accounts))
		now := time.Now()
		for _, u := range a.auth.accounts {
			expired := u.Enabled && u.ExpiresAt != "" && !u.activeAt(now)
			views = append(views, accountView{ID: u.ID, Username: u.Username, Role: u.Role, Enabled: u.Enabled, ExpiresAt: u.ExpiresAt, Expired: expired, Instances: append([]string{}, u.Instances...), ControlInstances: effectiveControlInstances(u), AllowedActions: effectiveOperatorActions(u), ReadPermissions: effectiveReadPermissions(u), AllowLogs: u.AllowLogs, LogInstances: effectiveLogInstances(u), RemoteInstances: append([]remoteGrant{}, u.RemoteInstances...)})
		}
		revision := a.auth.accountsRevision
		a.auth.mu.Unlock()
		reply(w, 200, map[string]any{"revision": revision, "accounts": views, "sampledAt": now.UTC().Format(time.RFC3339)})
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Action           string    `json:"action"`
		Revision         uint64    `json:"revision"`
		ID               string    `json:"id"`
		Username         string    `json:"username"`
		Role             string    `json:"role"`
		Enabled          bool      `json:"enabled"`
		ExpiresAt        *string   `json:"expiresAt"`
		Instances        []string  `json:"instances"`
		ControlInstances *[]string `json:"controlInstances"`
		AllowedActions   *[]string `json:"allowedActions"`
		ReadPermissions  *accountReadPermissions `json:"readPermissions"`
		AllowLogs        *bool     `json:"allowLogs"`
		LogInstances     *[]string `json:"logInstances"`
		RemoteInstances  *[]remoteGrant `json:"remoteInstances"`
		Password         string    `json:"password"`
	}
	if err := decode(w, r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	if req.Action != "save" && req.Action != "delete" {
		fail(w, 400, fmt.Errorf("未知账户操作"))
		return
	}
	var salt, hash, expiry string
	if req.Action == "save" {
		if req.ExpiresAt != nil {
			var err error
			expiry, err = normalizeAccountExpiry(*req.ExpiresAt)
			if err != nil {
				fail(w, 400, err)
				return
			}
		}
		if !accountNamePattern.MatchString(req.Username) || !validRole(req.Role) || !validGrants(req.Instances) || (req.Role == "admin" && len(req.Instances) != 0) || (req.ID == "" && req.Password == "") {
			fail(w, 400, fmt.Errorf("账户名称、角色、授权或密码无效"))
			return
		}
		if req.ControlInstances != nil && (!validGrants(*req.ControlInstances) || !subsetGrants(*req.ControlInstances, req.Instances) || (req.Role != "operator" && len(*req.ControlInstances) != 0)) {
			fail(w, 400, fmt.Errorf("可操作实例必须属于查看授权，且仅适用于操作员"))
			return
		}
		if req.AllowedActions != nil && (!validOperatorActions(*req.AllowedActions) || (req.Role != "operator" && len(*req.AllowedActions) != 0)) {
			fail(w, 400, fmt.Errorf("实例操作授权无效；仅操作员可以获得操作权限"))
			return
		}
		if req.LogInstances != nil && (!validGrants(*req.LogInstances) || !subsetGrants(*req.LogInstances, req.Instances) || (req.Role == "admin" && len(*req.LogInstances) != 0)) {
			fail(w, 400, fmt.Errorf("日志授权必须属于可查看实例"))
			return
		}
		if req.RemoteInstances != nil && (!validRemoteGrants(*req.RemoteInstances) || !validRemoteGrantRole(req.Role, *req.RemoteInstances)) {
			fail(w, 400, fmt.Errorf("远程实例授权无效"))
			return
		}
		if req.Password != "" {
			if !a.auth.acquireHash() {
				fail(w, 429, fmt.Errorf("账户操作繁忙，请稍后再试"))
				return
			}
			var err error
			salt, hash, err = newAccountHash(req.Password)
			a.auth.releaseHash()
			if err != nil {
				fail(w, 400, err)
				return
			}
		}
	}
	// Only the edited account waits for its already admitted requests. Password
	// derivation above happens before taking this gate or either state mutex.
	var targetGate *sync.RWMutex
	a.auth.mu.Lock()
	for _, u := range a.auth.accounts {
		if u.ID == req.ID && req.ID != "" {
			targetGate = u.gate
			break
		}
	}
	a.auth.mu.Unlock()
	if targetGate != nil {
		targetGate.Lock()
		defer targetGate.Unlock()
	}
	// Lock order is account gate, app, auth. Check grants against the same state used
	// when publishing them; empty grants mean no instances, never all.
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, id := range req.Instances {
		if _, ok := a.store.DB.State.Instance(id); !ok {
			fail(w, 400, fmt.Errorf("授权实例不存在"))
			return
		}
	}
	a.auth.mu.Lock()
	defer a.auth.mu.Unlock()
	if a.auth.accountLoadErr != nil {
		fail(w, 503, fmt.Errorf("账户文件无法读取，原文件已保留；请停止程序、备份并移走该文件后重启"))
		return
	}
	// Session revocation and role changes use this same lock. Keep it held
	// through the account write so authorization cannot change before commit.
	current, valid := a.auth.identityLocked(r)
	if !valid {
		fail(w, http.StatusUnauthorized, fmt.Errorf("请先登录"))
		return
	}
	if !current.admin() {
		fail(w, http.StatusForbidden, fmt.Errorf("没有此操作的权限"))
		return
	}
	if req.Revision != a.auth.accountsRevision {
		fail(w, 409, fmt.Errorf("账户已发生变化，请刷新后重试"))
		return
	}
	users := append([]localAccount(nil), a.auth.accounts...)
	index := -1
	for i, u := range users {
		if u.ID == req.ID && req.ID != "" {
			index = i
		}
		if req.Action == "save" && strings.EqualFold(u.Username, req.Username) && u.ID != req.ID {
			fail(w, 400, fmt.Errorf("用户名已存在"))
			return
		}
	}
	if index >= 0 && users[index].gate != targetGate {
		fail(w, 409, fmt.Errorf("账户已发生变化，请刷新后重试"))
		return
	}
	if req.Action == "delete" {
		if index < 0 {
			fail(w, 404, fmt.Errorf("账户不存在"))
			return
		}
		users = append(users[:index], users[index+1:]...)
		if err := a.auth.writeAccounts(users); err != nil {
			fail(w, 500, fmt.Errorf("无法保存账户配置"))
			return
		}
		a.auth.revoke(req.ID)
	} else {
		if req.ID != "" && index < 0 {
			fail(w, 404, fmt.Errorf("账户不存在"))
			return
		}
		if index < 0 && len(users) >= maxAccounts {
			fail(w, 400, fmt.Errorf("账户数量达到上限"))
			return
		}
		if index >= 0 && users[index].Username != req.Username {
			fail(w, 400, fmt.Errorf("不能更改现有用户名"))
			return
		}
		if index >= 0 && current.AccountID == req.ID && (req.Role != users[index].Role || len(req.Instances) != 0) {
			fail(w, http.StatusForbidden, fmt.Errorf("不能通过账户管理修改自己的权限"))
			return
		}
		u := localAccount{ID: req.ID, Username: req.Username, Role: req.Role, Enabled: req.Enabled, Instances: append([]string{}, req.Instances...), Salt: salt, Hash: hash}
		if req.ExpiresAt != nil {
			u.ExpiresAt = expiry
		} else if index >= 0 {
			u.ExpiresAt = users[index].ExpiresAt
		}
		if u.Role != "admin" {
			if req.ReadPermissions != nil {
				permissions := *req.ReadPermissions
				u.ReadPermissions = &permissions
			} else if index >= 0 && users[index].Role != "admin" && users[index].ReadPermissions != nil {
				permissions := *users[index].ReadPermissions
				u.ReadPermissions = &permissions
			}
			if req.RemoteInstances != nil {
				u.RemoteInstances = append([]remoteGrant{}, (*req.RemoteInstances)...)
			} else if index >= 0 {
				u.RemoteInstances = append([]remoteGrant{}, users[index].RemoteInstances...)
				if u.Role != "operator" {
					for i := range u.RemoteInstances {
						u.RemoteInstances[i].Control = false
					}
				}
			}
		}
		if u.Role == "operator" {
			if req.AllowedActions != nil {
				actions := append([]string{}, (*req.AllowedActions)...)
				u.AllowedActions = &actions
			} else if index >= 0 && users[index].Role == "operator" && users[index].AllowedActions != nil {
				actions := append([]string{}, (*users[index].AllowedActions)...)
				u.AllowedActions = &actions
			}
			if req.ControlInstances != nil {
				controls := append([]string{}, (*req.ControlInstances)...)
				u.ControlInstances = &controls
			} else if index >= 0 && users[index].ControlInstances != nil {
				// Older editors omit this field. Never retain a control grant after
				// its corresponding view grant has been removed.
				controls := retainGrants(*users[index].ControlInstances, u.Instances)
				u.ControlInstances = &controls
			}
		}
		// Older account editors omit this option; preserve it for existing users.
		if index >= 0 {
			u.AllowLogs = users[index].AllowLogs
		}
		if req.AllowLogs != nil {
			u.AllowLogs = *req.AllowLogs
		}
		if u.Role == "admin" {
			u.AllowLogs = false // administrators already have unrestricted access
		}
		if u.AllowLogs {
			if req.LogInstances != nil {
				logs := append([]string{}, (*req.LogInstances)...)
				u.LogInstances = &logs
			} else if index >= 0 && users[index].LogInstances != nil {
				logs := retainGrants(*users[index].LogInstances, u.Instances)
				u.LogInstances = &logs
			}
		} else if req.LogInstances != nil && len(*req.LogInstances) != 0 {
			fail(w, 400, fmt.Errorf("请先启用日志权限"))
			return
		}
		if index < 0 {
			u.ID = randomHex(16)
			u.gate = &sync.RWMutex{}
			users = append(users, u)
		} else {
			u.gate = users[index].gate
			if req.Password == "" {
				u.Salt, u.Hash = users[index].Salt, users[index].Hash
			}
			users[index] = u
		}
		if err := a.auth.writeAccounts(users); err != nil {
			fail(w, 500, fmt.Errorf("无法保存账户配置"))
			return
		}
		if index >= 0 {
			a.auth.revoke(u.ID)
		}
	}
	reply(w, 200, map[string]any{"ok": true, "revision": a.auth.accountsRevision})
}

func (a *App) passwordAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	p := requestPrincipal(r)
	if p.AccountID == "" {
		a.adminPasswordAPI(w, r, p)
		return
	}
	var req struct {
		Old string `json:"old"`
		New string `json:"new"`
	}
	if err := decode(w, r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	key := accountAttemptKey(p.Name)
	if err := a.auth.beginAttempt(key); err != nil {
		fail(w, 429, err)
		return
	}
	failed := false
	defer func() { a.auth.finishAttempt(key, failed) }()
	if len(req.Old) > 1024 {
		fail(w, 400, fmt.Errorf("原密码不正确"))
		return
	}
	a.auth.mu.Lock()
	var original localAccount
	for _, u := range a.auth.accounts {
		if u.ID == p.AccountID && u.Enabled {
			original = u
			break
		}
	}
	a.auth.mu.Unlock()
	if original.ID == "" {
		fail(w, 401, fmt.Errorf("请先登录"))
		return
	}
	if !a.auth.acquireHash() {
		fail(w, 429, fmt.Errorf("账户操作繁忙，请稍后再试"))
		return
	}
	hashHeld := true
	defer func() {
		if hashHeld {
			a.auth.releaseHash()
		}
	}()
	salt, _ := base64.RawStdEncoding.DecodeString(original.Salt)
	want, _ := base64.RawStdEncoding.DecodeString(original.Hash)
	got, err := passwordHash(req.Old, salt)
	if err != nil {
		fail(w, 400, fmt.Errorf("原密码不正确"))
		return
	}
	if subtle.ConstantTimeCompare(got, want) != 1 {
		failed = true
		fail(w, 400, fmt.Errorf("原密码不正确"))
		return
	}
	newSalt, newHash, err := newAccountHash(req.New)
	a.auth.releaseHash()
	hashHeld = false
	if err != nil {
		fail(w, 400, err)
		return
	}
	// Do not allow an in-flight password calculation to outlive a revocation.
	original.gate.Lock()
	defer original.gate.Unlock()
	a.auth.mu.Lock()
	defer a.auth.mu.Unlock()
	current, valid := a.auth.identityLocked(r)
	if !valid || current.AccountID != original.ID || current.gate != original.gate {
		fail(w, 401, fmt.Errorf("请先登录"))
		return
	}
	users := append([]localAccount(nil), a.auth.accounts...)
	for i, u := range users {
		if u.ID == original.ID && u.Enabled && u.Salt == original.Salt && u.Hash == original.Hash {
			users[i].Salt, users[i].Hash = newSalt, newHash
			if err := a.auth.writeAccounts(users); err != nil {
				fail(w, 500, fmt.Errorf("无法保存账户配置"))
				return
			}
			a.auth.revoke(u.ID)
			reply(w, 200, map[string]bool{"ok": true})
			return
		}
	}
	fail(w, 409, fmt.Errorf("账户已发生变化，请重新登录"))
}
