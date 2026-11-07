package manager
import("encoding/json";"net/http";"os";"path/filepath";"runtime";"strings";"testing")
func TestConfigDiffAPISecretRedaction(t *testing.T){
 a:=testApp(t);i:=seed(t,a)
 w:=request(a,"GET","/api/diff?id="+i.ID,nil,a.AccessToken(),"");if w.Code!=http.StatusConflict {t.Fatalf("missing baseline: %d",w.Code)}
 exe,e:=os.Executable();if e!=nil{t.Fatal(e)};b,e:=os.ReadFile(exe);if e!=nil{t.Fatal(e)}
 name:="frpc";if runtime.GOOS=="windows"{name+=".exe"};if e=os.WriteFile(filepath.Join(a.bin,name),b,0700);e!=nil{t.Fatal(e)}
 t.Setenv("FRP_TEST_HELPER","1")
 w=request(a,"POST","/api/control",map[string]any{"revision":a.store.DB.Revision,"id":i.ID,"action":"start"},a.AccessToken(),"");if w.Code!=200{t.Fatal(w.Body.String())}
 w=request(a,"POST","/api/control",map[string]any{"revision":a.store.DB.Revision,"id":i.ID,"action":"stop"},a.AccessToken(),"");if w.Code!=200{t.Fatal(w.Body.String())}
 v:=a.store.DB.State.public().Instances[0];v.Host="127.0.0.2";v.Port=7001;v.Token="replacement-secret-5678"
 w=request(a,"POST","/api/save",map[string]any{"revision":a.store.DB.Revision,"kind":"instance","value":v},a.AccessToken(),"");if w.Code!=200{t.Fatal(w.Body.String())}
 w=request(a,"GET","/api/diff?id="+i.ID,nil,a.AccessToken(),"");if w.Code!=200{t.Fatal(w.Body.String())}
 var got ConfigDiff;if e=json.Unmarshal(w.Body.Bytes(),&got);e!=nil{t.Fatal(e)}
 if !got.Changed||!got.SecretChanged||got.AddedCount==0||got.RemovedCount==0{t.Fatalf("%#v",got)}
 if strings.Contains(w.Body.String(),"test-secret-1234")||strings.Contains(w.Body.String(),"replacement-secret-5678"){t.Fatal("secret leaked")}
}
func TestDiffLinesBound(t *testing.T){
 before:=strings.Repeat("old\n",maxConfigDiffLines+2);after:=strings.Repeat("new\n",maxConfigDiffLines+1)
 r,a,rc,ac,tr:=diffLines(before,after);if rc!=maxConfigDiffLines+2||ac!=maxConfigDiffLines+1||!tr||len(r)!=maxConfigDiffLines||len(a)!=maxConfigDiffLines{t.Fatal("diff cap/count")}
}