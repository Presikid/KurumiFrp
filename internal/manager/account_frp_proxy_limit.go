package manager

import (
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/fatedier/frp/server"
)

const maxAccountFRPProxies = 1000000

// TryReserveProxy rechecks the credential and its account under the same
// admission gate as login. The slot lock is also acquired without waiting.
func (p *accountFRPProvider) TryReserveProxy(identity server.ManagedIdentity) (func(), error) {
	releaseAdmission, _, err := p.TryAuthorize(identity)
	if releaseAdmission != nil {
		defer releaseAdmission()
	}
	if err != nil || releaseAdmission == nil {
		return nil, errors.New("account proxy admission denied or busy")
	}
	auth := &p.app.auth // TryAuthorize holds App.mu and auth.mu here.
	var limit *int
	for _, account := range auth.accounts {
		if account.ID == identity.AccountID {
			limit = account.FRPProxyLimit
			break
		}
	}
	var once sync.Once
	releaseSlot := func() {
		once.Do(func() {
			auth.frpProxySlotsMu.Lock()
			defer auth.frpProxySlotsMu.Unlock()
			if auth.frpProxySlots[identity.AccountID] > 1 {
				auth.frpProxySlots[identity.AccountID]--
			} else {
				delete(auth.frpProxySlots, identity.AccountID)
			}
		})
	}
	if !auth.frpProxySlotsMu.TryLock() {
		return nil, errors.New("account proxy admission is busy")
	}
	defer auth.frpProxySlotsMu.Unlock()
	used := auth.frpProxySlots[identity.AccountID]
	if limit != nil && used >= *limit {
		return nil, errors.New("account TCP proxy limit reached")
	}
	if used < 0 || used == int(^uint(0)>>1) {
		return nil, errors.New("account proxy count unavailable")
	}
	if auth.frpProxySlots == nil {
		auth.frpProxySlots = make(map[string]int)
	}
	auth.frpProxySlots[identity.AccountID] = used + 1
	return releaseSlot, nil
}

// The account file is the only persistent state. Active slots are process-local
// and remain associated with the stable account ID until each proxy closes.
func (a *App) accountFRPProxyLimitAPI(w http.ResponseWriter, r *http.Request, revision uint64, id string, limited *bool, limit *int) {
	if !accountIDPattern.MatchString(id) || limited == nil || (*limited && (limit == nil || *limit < 0 || *limit > maxAccountFRPProxies)) || (!*limited && limit != nil) {
		fail(w, http.StatusBadRequest, fmt.Errorf("受管 TCP 代理上限无效"))
		return
	}
	status, nextRevision, err := func() (int, uint64, error) {
		a.auth.mu.Lock()
		defer a.auth.mu.Unlock()
		if a.auth.accountLoadErr != nil {
			return http.StatusServiceUnavailable, 0, fmt.Errorf("账户文件无法读取；原文件已保留")
		}
		current, valid := a.auth.identityLocked(r)
		if !valid {
			return http.StatusUnauthorized, 0, fmt.Errorf("请先登录")
		}
		if !current.admin() {
			return http.StatusForbidden, 0, fmt.Errorf("没有此操作的权限")
		}
		if a.auth.accountsWriteUncertain {
			return http.StatusServiceUnavailable, 0, a.auth.accountWriteFailure()
		}
		if revision != a.auth.accountsRevision {
			return http.StatusConflict, 0, fmt.Errorf("账户已发生变化，请刷新后重试")
		}
		users := append([]localAccount(nil), a.auth.accounts...)
		index := -1
		for i := range users {
			if users[i].ID == id {
				index = i
				break
			}
		}
		if index < 0 {
			return http.StatusNotFound, 0, fmt.Errorf("账户不存在")
		}
		if *limited {
			value := *limit
			users[index].FRPProxyLimit = &value
		} else {
			users[index].FRPProxyLimit = nil
		}
		if err := a.auth.writeAccounts(users); err != nil {
			return http.StatusInternalServerError, 0, a.auth.accountWriteFailure()
		}
		return http.StatusOK, a.auth.accountsRevision, nil
	}()
	if err != nil {
		fail(w, status, err)
		return
	}
	reply(w, http.StatusOK, map[string]any{"ok": true, "revision": nextRevision})
}
