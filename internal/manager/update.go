package manager

import (
 "fmt"
 "net/http"
)
func (a *App) updateAPI(w http.ResponseWriter, r *http.Request) {
 switch {
 case r.Method=="GET" && r.URL.Path=="/api/update/releases":
  releases,e:=a.updater.Versions(r.Context());if e!=nil{fail(w,502,e);return};reply(w,200,map[string]any{"versions":releases,"status":a.updater.Status()})
 case r.Method=="GET" && r.URL.Path=="/api/update/status":
  reply(w,200,map[string]string{"status":a.updater.Status()})
 case r.Method=="POST" && r.URL.Path=="/api/update/install":
  var req struct {Tag string `json:"tag"`};if e:=decode(w,r,&req);e!=nil{fail(w,400,e);return};if e:=a.updater.Start(req.Tag);e!=nil{fail(w,400,e);return};reply(w,202,map[string]bool{"ok":true})
 default: fail(w,404,fmt.Errorf("更新接口不存在"))
 }
}
