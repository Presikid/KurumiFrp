//go:build linux

package update

import (
 "context"
 "fmt"
 "io"
 "os"
 "os/exec"
 "path/filepath"
 "strings"
 "syscall"
 "time"
)
const updateRequest = "/var/lib/kurumifrp/update.request"
func packageDistribution() string {
 if e:=exec.Command("dpkg-query","-W","-f=${Status}","kurumifrp").Run();e==nil{return "linux-deb"}
 if e:=exec.Command("rpm","-q","kurumifrp").Run();e==nil{return "linux-rpm"};return ""
}
func DetectDistribution(distribution string) string {if distribution=="linux-system" {return packageDistribution()};return distribution}
func requestSystemUpdate(tag string) error {
 if _,e:=os.Stat("/run/systemd/system");e!=nil{return fmt.Errorf("需要运行中的 systemd 更新服务")}
 if e:=exec.Command("systemctl","is-active","--quiet","kurumifrp-updater.path").Run();e!=nil{return fmt.Errorf("系统更新服务未启用")}
 temp,e:=os.CreateTemp(filepath.Dir(updateRequest),".update-request-");if e!=nil{return e};defer os.Remove(temp.Name()); _,e=temp.WriteString(tag);if e==nil{e=temp.Sync()};if ce:=temp.Close();e==nil{e=ce};if e!=nil{return e};return os.Rename(temp.Name(),updateRequest)
}
// Executed by the root-owned systemd unit; the unprivileged request only selects
// a version. All release metadata, package bytes and hashes are obtained again.
func PerformSystemUpdate() error {
 if os.Geteuid()!=0{return fmt.Errorf("system update requires root")}
 fd,e:=syscall.Open(updateRequest,syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK,0);if e!=nil{return e}
 f:=os.NewFile(uintptr(fd),updateRequest);defer f.Close();info,e:=f.Stat();if e!=nil{return e};if !info.Mode().IsRegular() || info.Size()>64{return fmt.Errorf("invalid update request")}
 data,e:=io.ReadAll(io.LimitReader(f,65));if e!=nil||len(data)>64{return fmt.Errorf("invalid update request")};if e=os.Remove(updateRequest);e!=nil{return e};tag:=strings.TrimSpace(string(data))
 distribution:=packageDistribution();if distribution=="" {return fmt.Errorf("installed package not found")};m:=New("",distribution);ctx,cancel:=context.WithTimeout(context.Background(),10*time.Minute);defer cancel();a,e:=m.selectAsset(ctx,tag);if e!=nil{return e}
 dir,e:=os.MkdirTemp("","kurumi-update-root-");if e!=nil{return e};defer os.RemoveAll(dir);path,e:=m.download(ctx,a,dir);if e!=nil{return e}
 var cmd *exec.Cmd;if distribution=="linux-deb"{cmd=exec.CommandContext(ctx,"apt-get","-y","install",path);cmd.Env=append(os.Environ(),"DEBIAN_FRONTEND=noninteractive")}else{cmd=exec.CommandContext(ctx,"dnf","-y","install",path)}
 cmd.Stdout=os.Stdout;cmd.Stderr=os.Stderr;return cmd.Run()
}
