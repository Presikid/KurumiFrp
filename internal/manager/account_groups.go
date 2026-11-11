package manager

import (
	"fmt"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxAccountGroups = 100

// Groups are organizational labels. They never grant permissions or change
// account expiry. Empty GroupID means that an account is ungrouped.
type accountGroup struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func normalizeAccountGroupName(value string) (string, error) {
	if !utf8.ValidString(value) {
		return "", fmt.Errorf("分组名称无效")
	}
	name := strings.TrimSpace(value)
	if name == "" || len(name) > 128 || utf8.RuneCountInString(name) > 40 {
		return "", fmt.Errorf("分组名称须为 1 至 40 个字符，且不超过 128 字节")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("分组名称不能包含控制字符")
		}
	}
	return name, nil
}

func validateAccountGroups(groups []accountGroup, accounts []localAccount) error {
	if len(groups) > maxAccountGroups {
		return fmt.Errorf("账户分组数量达到上限")
	}
	ids := make(map[string]bool, len(groups))
	for i, group := range groups {
		name, err := normalizeAccountGroupName(group.Name)
		if err != nil || name != group.Name || !accountIDPattern.MatchString(group.ID) || ids[group.ID] {
			return fmt.Errorf("账户分组无效")
		}
		for _, previous := range groups[:i] {
			if strings.EqualFold(previous.Name, name) {
				return fmt.Errorf("分组名称已存在")
			}
		}
		ids[group.ID] = true
	}
	for _, account := range accounts {
		if account.GroupID != "" && !ids[account.GroupID] {
			return fmt.Errorf("账户引用了不存在的分组")
		}
	}
	return nil
}

// Group mutations share the account revision and file. They deliberately do
// not revoke sessions: only metadata changes, never identity or permissions.
func (a *App) accountGroupAction(w http.ResponseWriter, r *http.Request, action string, revision uint64, id string, groupID *string, groupName string) {
	a.mu.Lock()
	a.auth.mu.Lock()
	status, mutationErr := func() (int, error) {
		if a.closing {
			return http.StatusConflict, fmt.Errorf("工作台正在关闭")
		}
		if a.auth.accountLoadErr != nil {
			return http.StatusServiceUnavailable, fmt.Errorf("账户文件无法读取，原文件已保留")
		}
		current, valid := a.auth.identityLocked(r)
		if !valid {
			return http.StatusUnauthorized, fmt.Errorf("请先登录")
		}
		if !current.admin() {
			return http.StatusForbidden, fmt.Errorf("仅管理员可管理账户分组")
		}
		if revision != a.auth.accountsRevision {
			return http.StatusConflict, fmt.Errorf("账户或分组已发生变化，请刷新后重试")
		}
		groups := append([]accountGroup{}, a.auth.accountGroups...)
		users := append([]localAccount{}, a.auth.accounts...)
		switch action {
		case "group-save":
			name, err := normalizeAccountGroupName(groupName)
			if err != nil {
				return http.StatusBadRequest, err
			}
			index := -1
			for i, group := range groups {
				if group.ID == id && id != "" {
					index = i
				} else if strings.EqualFold(group.Name, name) {
					return http.StatusBadRequest, fmt.Errorf("分组名称已存在")
				}
			}
			if id != "" && index < 0 {
				return http.StatusNotFound, fmt.Errorf("分组不存在")
			}
			if index < 0 {
				if len(groups) >= maxAccountGroups {
					return http.StatusBadRequest, fmt.Errorf("账户分组数量达到上限")
				}
				for attempts := 0; attempts < 4; attempts++ {
					candidate := randomHex(16)
					unique := true
					for _, group := range groups {
						if group.ID == candidate {
							unique = false
							break
						}
					}
					if unique {
						id = candidate
						break
					}
				}
				if id == "" {
					return http.StatusInternalServerError, fmt.Errorf("无法创建分组")
				}
				groups = append(groups, accountGroup{ID: id, Name: name})
			} else if groups[index].Name == name {
				return http.StatusOK, nil
			} else {
				groups[index].Name = name
			}
		case "group-delete":
			index := -1
			for i, group := range groups {
				if group.ID == id {
					index = i
					break
				}
			}
			if index < 0 {
				return http.StatusNotFound, fmt.Errorf("分组不存在")
			}
			groups = append(groups[:index], groups[index+1:]...)
			for i := range users {
				if users[i].GroupID == id {
					users[i].GroupID = ""
				}
			}
		case "group-assign":
			if groupID == nil {
				return http.StatusBadRequest, fmt.Errorf("请指定账户分组")
			}
			foundGroup := *groupID == ""
			for _, group := range groups {
				if group.ID == *groupID {
					foundGroup = true
					break
				}
			}
			if !foundGroup {
				return http.StatusBadRequest, fmt.Errorf("分组不存在，请刷新后重试")
			}
			index := -1
			for i, user := range users {
				if user.ID == id {
					index = i
					break
				}
			}
			if index < 0 {
				return http.StatusNotFound, fmt.Errorf("账户不存在")
			}
			if users[index].GroupID == *groupID {
				return http.StatusOK, nil
			}
			users[index].GroupID = *groupID
		default:
			return http.StatusBadRequest, fmt.Errorf("未知分组操作")
		}
		if err := a.auth.writeAccountsAndGroups(users, groups); err != nil {
			return http.StatusInternalServerError, fmt.Errorf("无法保存账户分组")
		}
		return http.StatusOK, nil
	}()
	newRevision := a.auth.accountsRevision
	a.auth.mu.Unlock()
	a.mu.Unlock()
	if mutationErr != nil {
		fail(w, status, mutationErr)
		return
	}
	reply(w, http.StatusOK, map[string]any{"ok": true, "revision": newRevision})
}
