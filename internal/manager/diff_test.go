package manager

import (
 "encoding/json"
 "os"
 "path/filepath"
 "runtime"
 "strings"
 "testing"
)

func TestCompareConfigLines(t *testing.T) {
 old := "# generated\nname = \"web\"\nremotePort = 6000\nremotePort = 6000\n"
 next := "remotePort = 6000\nname = \"web\"\nremotePort = 7000\n# updated\n"
 added, removed, ac, rc, truncated := compareConfigLines(old,next,10)
 if ac != 1 || rc != 1 || truncated || len(added)!=1 || added[0] != "remotePort = 7000" || len(removed)!=1 || removed[0] != "remotePort = 6000" { t.Fatalf("%#v %#v %d %d %v",added,removed,ac,rc,truncated) }
 added,removed,ac,rc,_ = compareConfigLines("a = 1\nb = 2","b = 2\na = 1",10)
 if ac != 0 || rc != 0 || len(added)!=0 || len(removed)!=0 { t.Fatal("reordering should not create diff") }
 _,_,ac,_,truncated = compareConfigLines("","a\nb\nc",1)
 if ac != 3 || !truncated { t.Fatal("truncation failed") }
}

func TestConfigDiffPreviewAPI(t *testing.T) {
 a:=testApp(t); instance:=seed(t,a); path:="/api/diff?id="+instance.ID
 if w:=request(a,"GET",path,nil,"",""); w.Code!=401 { t.Fatalf("unauthenticated: %d",w.Code) }
 if w:=request(a,"GET",path,nil,a.AccessToken(),""); w.Code!=409 { t.Fatalf("stopped: %d",w.Code) }
 exe,err:=os.Executable(); if err!=nil { t.Fatal(err) }
 b,err:=os.ReadFile(exe); if err!=nil { t.Fatal(err) }
 name:="frpc"; if runtime.GOOS=="windows" { name+=".exe" }
 if err=os.WriteFile(filepath.Join(a.bin,name),b,0700); err!=nil { t.Fatal(err) }
 t.Setenv("FRP_TEST_HELPER","1")
 w:=request(a,"POST","/api/control",map[string]any{"revision":a.store.DB.Revision,"id":instance.ID,"action":"start"},a.AccessToken(),"")
 if w.Code!=200 { t.Fatalf("start: %d %s",w.Code,w.Body.String()) }
 w=request(a,"GET",path,nil,a.AccessToken(),"")
 var initial ConfigDiff
 if w.Code!=200 || json.Unmarshal(w.Body.Bytes(),&initial)!=nil || initial.Pending { t.Fatalf("initial diff: %d %s",w.Code,w.Body.String()) }

 pub:=a.store.DB.State.public().Instances[0]; pub.Token="changed-secret-9876"
 w=request(a,"POST","/api/save",map[string]any{"revision":a.store.DB.Revision,"kind":"instance","value":pub},a.AccessToken(),"")
 if w.Code!=200 { t.Fatal(w.Body.String()) }
 w=request(a,"GET",path,nil,a.AccessToken(),"")
 var secret ConfigDiff
 if w.Code!=200 || json.Unmarshal(w.Body.Bytes(),&secret)!=nil || !secret.Pending || !secret.SecretOnly || strings.Contains(w.Body.String(),"changed-secret-9876") || strings.Contains(w.Body.String(),"test-secret-1234") { t.Fatalf("unsafe secret diff: %s",w.Body.String()) }

 pub=a.store.DB.State.public().Instances[0]; pub.Token="test-secret-1234"
 w=request(a,"POST","/api/save",map[string]any{"revision":a.store.DB.Revision,"kind":"instance","value":pub},a.AccessToken(),"")
 if w.Code!=200 { t.Fatal(w.Body.String()) }
 st:=a.store.DB.State.clone()
 st.Tunnels=append(st.Tunnels,Tunnel{ID:randomHex(8),InstanceID:instance.ID,Name:"web",Type:"tcp",LocalHost:"127.0.0.1",LocalPort:8080,RemotePort:6000,Enabled:true})
 if err:=a.store.commit(st,"add pending rule"); err!=nil { t.Fatal(err) }
 w=request(a,"GET",path,nil,a.AccessToken(),"")
 var d ConfigDiff
 if w.Code!=200 || json.Unmarshal(w.Body.Bytes(),&d)!=nil || !d.Pending || d.AddedCount==0 || d.RemovedCount!=0 || !strings.Contains(strings.Join(d.Added,"\n"),"name = \"web\"") || strings.Contains(w.Body.String(),"test-secret-1234") { t.Fatalf("bad config diff: %d %s",w.Code,w.Body.String()) }
 w=request(a,"POST","/api/control",map[string]any{"revision":a.store.DB.Revision,"id":instance.ID,"action":"stop"},a.AccessToken(),"")
 if w.Code!=200 { t.Fatal(w.Body.String()) }
 if w=request(a,"GET",path,nil,a.AccessToken(),""); w.Code!=409 { t.Fatalf("stopped diff: %d",w.Code) }
}
