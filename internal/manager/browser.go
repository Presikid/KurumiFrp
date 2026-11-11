package manager

import (
	"fmt"
	"net/http"
)

// Opening the browser never authenticates it; the administrator password is
// required even when the page was opened by a local shortcut.
func (a *App) BrowserURL() string {
	return "http://" + a.addr + "/"
}
func (a *App) launchLogin(w http.ResponseWriter, r *http.Request) {
	a.loginHistory.record(r, "启动凭据", false)
	fail(w, http.StatusUnauthorized, fmt.Errorf("请使用密码登录"))
}
