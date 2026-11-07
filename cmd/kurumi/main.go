package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Presikid/KurumiFrp/internal/manager"
	"github.com/Presikid/KurumiFrp/web"
)

func main() {
	if e := run(); e != nil {
		log.Fatal(e)
	}
}
func run() error {
	dir := flag.String("data", "./data", "private persistent data directory")
	bin := flag.String("frp-dir", "./frp", "directory with trusted frpc/frps binaries")
	addr := flag.String("listen", "127.0.0.1:9000", "loopback management listener, or auto for ports 9000..9010")
	open := flag.Bool("open", false, "open local WebUI with a one-time login ticket")
	version := flag.Bool("version", false, "print version and exit")
	stop := flag.Bool("stop", false, "stop this data directory's manager and wait for owned frp processes")
	check := flag.Bool("check-running", false, "verify the installed manager is responding")
	archive := flag.String("cleanup-archive", "", "explicit original release archive, verified before deletion")
	cleanup := flag.String("cleanup-package", "", "remove a verified extracted release payload after installation")
	token := flag.Bool("show-token", false, "print existing local access token and exit")
	flag.Parse()
	if *cleanup != "" {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		return cleanPackage(*cleanup, exe, *archive)
	}
	if *version {
		fmt.Println("0.3.0")
		return nil
	}
	abs, e := filepath.Abs(*dir)
	if e != nil {
		return e
	}
	if *stop {
		return stopExisting(abs, 45*time.Second)
	}
	if *check {
		return checkRunning(abs, 20*time.Second)
	}
	if err := rejectUninstall(abs); err != nil {
		return err
	}
	if *token {
		b, e := os.ReadFile(filepath.Join(abs, "access.token"))
		if e != nil {
			return e
		}
		fmt.Println(string(b))
		return nil
	}
	if *addr == "auto" {
		chosen, err := autoListen(abs)
		if err != nil {
			return err
		}
		*addr = chosen
	}
	if e = os.MkdirAll(abs, 0700); e != nil {
		return e
	}
	if st, err := os.Lstat(abs); err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("unsafe data directory")
	}
	lock := filepath.Join(abs, "manager.lock")
	f, e := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		if *open {
			if b, err := os.ReadFile(filepath.Join(abs, "access.token")); err == nil {
				if err = openExisting(*addr, string(b)); err == nil {
					return nil
				}
			}
		}
		return fmt.Errorf("data directory locked; verify no old manager/frp process is alive before removing manager.lock")
	}
	fmt.Fprintln(f, os.Getpid())
	f.Close()
	cleanShutdown := true
	defer func() {
		if cleanShutdown {
			_ = os.Remove(lock)
		}
	}()
	if err := rejectUninstall(abs); err != nil {
		return err
	}
	app, e := manager.New(abs, *bin, *addr, web.Files)
	if e != nil {
		return e
	}
	defer func() {
		if err := app.Close(); err != nil {
			cleanShutdown = false
			log.Printf("Shutdown incomplete; retained manager.lock: %v", err)
		}
	}()
	listener, e := net.Listen("tcp", *addr)
	if e != nil {
		return e
	}
	defer listener.Close()
	if e = saveListen(abs, *addr); e != nil {
		return e
	}
	server := manager.HTTPServer(*addr, app)
	stopped := make(chan os.Signal, 1)
	signal.Notify(stopped, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stopped)
	errs := make(chan error, 1)
	go func() { errs <- server.Serve(listener) }()
	fmt.Printf("KurumiFRP 0.3.0\nOpen http://%s\nGet local token in another terminal: kurumi --data %q --show-token\n", *addr, abs)
	if *open {
		if e := openBrowser(app.BrowserURL()); e != nil {
			log.Print("Browser could not be opened; use the printed local URL and --show-token.")
		}
	}
	select {
	case <-app.Done():
	case <-stopped:
	case e = <-errs:
		if e != nil && e != http.ErrServerClosed {
			return e
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return server.Shutdown(ctx)
}
