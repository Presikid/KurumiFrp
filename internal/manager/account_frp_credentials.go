package manager

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"
)

// These records are administrative preparations only. No runtime consumes
// them, and a registered token does not enable account authentication.
const maxAccountFRPTokens = 64

type accountFRPToken struct {
	InstanceID   string `json:"instanceId"`
	Incarnation  string `json:"incarnation"`
	CredentialID string `json:"credentialId"`
	Version      uint64 `json:"version"`
	Encrypted    string `json:"encryptedToken"`
}

type accountFRPTokenView struct {
	InstanceID   string `json:"instanceId"`
	CredentialID string `json:"credentialId"`
	Version      uint64 `json:"version"`
	Status       string `json:"status"`
}

type accountFRPInstanceView struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Binding string `json:"binding"`
}

// Caller holds App.mu. The binding is an opaque observation of the current
// incarnation, not a credential or a grant.
func (a *App) accountFRPInstancesLocked() []accountFRPInstanceView {
	instances := make([]accountFRPInstanceView, 0)
	for _, instance := range a.store.DB.State.Instances {
		if instance.Kind == "frps" {
			instances = append(instances, accountFRPInstanceView{ID: instance.ID, Name: instance.Name, Binding: nodeInstanceBinding(a.store.DB.LocalTrafficIDs[instance.ID])})
		}
	}
	return instances
}

// Caller holds App.mu and auth.mu. An unreadable or stale record affects only
// this view; it must not disable ordinary account authentication.
func (a *App) accountFRPTokenViewsLocked(user localAccount) []accountFRPTokenView {
	if len(user.FRPTokens) == 0 {
		return nil
	}
	counts := make(map[string]int, len(user.FRPTokens))
	for _, token := range user.FRPTokens {
		counts[token.InstanceID]++
	}
	views := make([]accountFRPTokenView, 0, len(user.FRPTokens))
	for _, token := range user.FRPTokens {
		view := accountFRPTokenView{InstanceID: token.InstanceID, CredentialID: token.CredentialID, Version: token.Version, Status: "needs-reissue"}
		instance, exists := a.store.DB.State.Instance(token.InstanceID)
		if len(user.FRPTokens) <= maxAccountFRPTokens && counts[token.InstanceID] == 1 && idPattern.MatchString(token.InstanceID) &&
			accountIDPattern.MatchString(token.CredentialID) && token.Version > 0 && token.Version <= maxJSONRevision &&
			validLocalTrafficID(token.Incarnation) && exists && instance.Kind == "frps" && a.store.DB.LocalTrafficIDs[token.InstanceID] == token.Incarnation {
			plain, err := a.store.decrypt(token.Encrypted)
			if err == nil && nodeTokenPattern.MatchString(plain) {
				view.Status = "registered"
			}
		}
		views = append(views, view)
	}
	return views
}

func accountFRPRandomHex(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

func (a *App) accountFRPTokenAPI(w http.ResponseWriter, r *http.Request, action string, revision uint64, configRevision *uint64, accountID, instanceID, instanceBinding string) {
	var revocation *accountFRPRevocation
	result, status, err := a.changeAccountFRPToken(r, action, revision, configRevision, accountID, instanceID, instanceBinding, &revocation)
	if err != nil {
		fail(w, status, err)
		return
	}
	if revocation != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if a.finishAccountFRPRevocation(ctx, revocation) {
			result["revocation"] = "complete"
		} else {
			result["revocation"] = "unconfirmed"
			result["warning"] = "凭据已修改，但旧连接关闭状态未确认；已请求停止实例，请核对实例并安全重启工作台"
		}
	}
	reply(w, status, result)
}

// Acquire the same target gate and lock order as ordinary account changes.
// Any affected live credential is blocked under this gate before the caller
// waits for its connections to close without holding manager locks.
func (a *App) changeAccountFRPToken(r *http.Request, action string, revision uint64, configRevision *uint64, accountID, instanceID, instanceBinding string, revocation **accountFRPRevocation) (map[string]any, int, error) {
	if !accountIDPattern.MatchString(accountID) || !idPattern.MatchString(instanceID) {
		return nil, http.StatusBadRequest, fmt.Errorf("账户或实例标识无效")
	}
	var plain, freshID string
	if action != "frp-disable" {
		var err error
		plain, err = accountFRPRandomHex(32)
		if err == nil {
			freshID, err = accountFRPRandomHex(16)
		}
		if err != nil {
			return nil, http.StatusServiceUnavailable, fmt.Errorf("无法生成 frp 凭据")
		}
	}
	a.auth.mu.Lock()
	var gateFound bool
	var target localAccount
	for _, user := range a.auth.accounts {
		if user.ID == accountID {
			target, gateFound = user, true
			break
		}
	}
	a.auth.mu.Unlock()
	if !gateFound {
		return nil, http.StatusNotFound, fmt.Errorf("账户不存在")
	}
	if target.gate == nil {
		return nil, http.StatusServiceUnavailable, fmt.Errorf("账户状态不可用")
	}
	target.gate.Lock()
	defer target.gate.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.auth.mu.Lock()
	defer a.auth.mu.Unlock()
	if a.closing || a.auth.accountLoadErr != nil {
		return nil, http.StatusServiceUnavailable, fmt.Errorf("账户状态不可用")
	}
	if a.auth.accountsWriteUncertain {
		return nil, http.StatusServiceUnavailable, fmt.Errorf("账户文件写入状态未确认，请安全重启工作台后核对")
	}
	if a.accountFRPRevocations[accountFRPRevocationKey{accountID, instanceID}] != nil {
		return nil, http.StatusConflict, fmt.Errorf("该账户的旧连接尚未确认关闭，请安全重启工作台后核对")
	}
	current, valid := a.auth.identityLocked(r)
	if !valid {
		return nil, http.StatusUnauthorized, fmt.Errorf("请先登录")
	}
	if !current.admin() {
		return nil, http.StatusForbidden, fmt.Errorf("仅管理员可管理 frp 凭据")
	}
	if revision != a.auth.accountsRevision {
		return nil, http.StatusConflict, fmt.Errorf("账户已发生变化，请刷新后重试")
	}
	if action != "frp-disable" && (configRevision == nil || *configRevision != a.store.DB.Revision) {
		return nil, http.StatusConflict, fmt.Errorf("实例配置已发生变化，请刷新后重试")
	}
	users := append([]localAccount(nil), a.auth.accounts...)
	index := -1
	for i, user := range users {
		if user.ID == accountID {
			index = i
			break
		}
	}
	if index < 0 || users[index].gate != target.gate {
		return nil, http.StatusConflict, fmt.Errorf("账户已发生变化，请刷新后重试")
	}
	entries := append([]accountFRPToken(nil), users[index].FRPTokens...)
	matching, entryIndex := 0, -1
	for i, token := range entries {
		if token.InstanceID == instanceID {
			matching++
			entryIndex = i
		}
	}
	if action == "frp-disable" {
		if matching == 0 {
			return nil, http.StatusNotFound, fmt.Errorf("没有已登记的 frp 凭据")
		}
		kept := make([]accountFRPToken, 0, len(entries)-matching)
		for _, token := range entries {
			if token.InstanceID != instanceID {
				kept = append(kept, token)
			}
		}
		entries = kept
	} else {
		if len(entries) > maxAccountFRPTokens {
			return nil, http.StatusConflict, fmt.Errorf("该账户的 frp 凭据记录过多，请先停用不再使用的记录")
		}
		if action == "frp-issue" && matching != 0 {
			return nil, http.StatusConflict, fmt.Errorf("该实例已有凭据，请轮换或停用")
		}
		if action == "frp-rotate" && matching != 1 {
			return nil, http.StatusConflict, fmt.Errorf("凭据记录不存在或不唯一，请刷新核对")
		}
		if action == "frp-issue" && len(entries) >= maxAccountFRPTokens {
			return nil, http.StatusConflict, fmt.Errorf("该账户的 frp 凭据已达上限，请先停用不再使用的记录")
		}
		instance, exists := a.store.DB.State.Instance(instanceID)
		incarnation := a.store.DB.LocalTrafficIDs[instanceID]
		if !exists || instance.Kind != "frps" || !validLocalTrafficID(incarnation) {
			return nil, http.StatusConflict, fmt.Errorf("请选择当前本机 frps 实例")
		}
		if !nodeTokenPattern.MatchString(instanceBinding) || nodeInstanceBinding(incarnation) != instanceBinding {
			return nil, http.StatusConflict, fmt.Errorf("实例已被替换，请重新核对并选择")
		}
		for _, user := range users {
			for _, existing := range user.FRPTokens {
				if existing.CredentialID == freshID {
					return nil, http.StatusServiceUnavailable, fmt.Errorf("无法生成唯一的 frp 凭据，请重试")
				}
				if decoded, err := a.store.decrypt(existing.Encrypted); err == nil && decoded == plain {
					return nil, http.StatusServiceUnavailable, fmt.Errorf("无法生成唯一的 frp 凭据，请重试")
				}
			}
		}
		credentialID, version := freshID, uint64(1)
		if action == "frp-rotate" {
			previous := entries[entryIndex]
			if previous.Incarnation == incarnation && accountIDPattern.MatchString(previous.CredentialID) && previous.Version > 0 {
				if previous.Version >= maxJSONRevision {
					return nil, http.StatusConflict, fmt.Errorf("凭据版本已达上限，请先停用再重新签发")
				}
				// A new ID also distinguishes retries after an uncertain write;
				// the prior on-disk version might already have been replaced.
				version = previous.Version + 1
			}
		}
		encrypted, err := a.store.encrypt(plain)
		if err != nil {
			return nil, http.StatusInternalServerError, fmt.Errorf("无法保护 frp 凭据")
		}
		token := accountFRPToken{InstanceID: instanceID, Incarnation: incarnation, CredentialID: credentialID, Version: version, Encrypted: encrypted}
		if action == "frp-issue" {
			entries = append(entries, token)
		} else {
			entries[entryIndex] = token
		}
	}
	if r.Context().Err() != nil {
		return nil, http.StatusRequestTimeout, fmt.Errorf("请求已取消，请刷新核对")
	}
	var pending *accountFRPRevocation
	if action != "frp-issue" {
		pending = a.accountFRPRevocationLocked(users[index], instanceID)
	}
	users[index].FRPTokens = entries
	if err := a.auth.writeAccounts(users); err != nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("保存结果未确认，请安全停止并重启工作台后核对")
	}
	a.publishAccountFRPRevocationLocked(pending)
	*revocation = pending
	result := map[string]any{"ok": true, "revision": a.auth.accountsRevision, "configRevision": a.store.DB.Revision}
	if action != "frp-disable" {
		views := a.accountFRPTokenViewsLocked(users[index])
		for _, view := range views {
			if view.InstanceID == instanceID {
				result["credential"] = view
				break
			}
		}
		result["token"] = plain
	}
	return result, http.StatusOK, nil
}
