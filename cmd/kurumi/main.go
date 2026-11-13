package main

import (
	"context"
	"encoding/json"
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
	"github.com/Presikid/KurumiFrp/internal/update"
	"github.com/Presikid/KurumiFrp/internal/version"
	"github.com/Presikid/KurumiFrp/web"
)

func main() {
	if e := run(); e != nil {
		reportDesktopError(e)
		log.Fatal(e)
	}
}
func run() error {
	defaultData, defaultListen, pathErr := defaultPaths()
	if pathErr != nil {
		return pathErr
	}
	dir := flag.String("data", defaultData, "private persistent data directory")
	addr := flag.String("listen", defaultListen, "loopback management listener, or auto for ports 9000..9010")
	paths := flag.Bool("paths", false, "show resolved native program, data and temporary paths")
	open := flag.Bool("open", false, "open local WebUI")
	showVersion := flag.Bool("version", false, "print version and exit")
	installDir := flag.String("install-dir", "", "installer maintenance target")
	stop := flag.Bool("stop", false, "stop this data directory's manager and wait for managed instances")
	check := flag.Bool("check-running", false, "verify the installed manager is responding")
	archive := flag.String("cleanup-archive", "", "explicit original release archive, verified before deletion")
	cleanup := flag.String("cleanup-package", "", "remove a verified extracted release payload after installation")
	token := flag.Bool("show-token", false, "print existing local access token and exit")
	native := flag.String("native-maintenance", "", "native installer maintenance operation")
	systemUpdate := flag.Bool("system-update", false, "perform a verified Linux package update")
	setup := flag.String("cleanup-setup", "", "exact native Setup executable to remove after successful installation")
	digest := flag.String("cleanup-sha256", "", "expected Setup SHA-256")
	flag.Parse()
	if *systemUpdate {
		if distribution != "linux-system" {
			return fmt.Errorf("system update requires a Linux system installation")
		}
		return update.PerformSystemUpdate()
	}
	if *native != "" {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		return installedOperation(exe, *native, *installDir)
	}
	if *setup != "" {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		return removeSetup(*setup, *digest, exe, 90*time.Second)
	}
	if *cleanup != "" {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		return cleanPackage(*cleanup, exe, *archive)
	}
	if *showVersion {
		fmt.Println(version.Current)
		return nil
	}
	if *paths {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"program": filepath.Dir(exe), "data": defaultData, "temporary": temporaryRoot(defaultData), "distribution": distribution})
	}
	if distribution == "windows-installed" {
		if *dir != defaultData {
			return fmt.Errorf("installed builds keep runtime data under the installation directory; use a source build for custom paths")
		}
		// Setup starts the unelevated manager asynchronously. Its read-only
		// readiness probe may arrive before that account creates its profile.
		until := time.Now().Add(20 * time.Second)
		for {
			err := prepareInstalled(!*stop && !*check && !*token)
			if err == nil {
				break
			}
			if !*check || !os.IsNotExist(err) || time.Now().After(until) {
				return err
			}
			time.Sleep(100 * time.Millisecond)
		}
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
	_, writeErr := fmt.Fprintln(f, os.Getpid())
	closeErr := f.Close()
	if writeErr != nil {
		return fmt.Errorf("manager lock write failed; startup aborted without removing the lock: %w", writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("manager lock close failed; startup aborted without removing the lock: %w", closeErr)
	}
	cleanShutdown := true
	defer func() {
		if cleanShutdown {
			_ = os.Remove(lock)
		}
	}()
	if err := rejectUninstall(abs); err != nil {
		return err
	}
	cleanTemp, e := prepareTemporary(abs)
	if e != nil {
		return e
	}
	defer cleanTemp()
	app, e := manager.NewWithRuntime(abs, *addr, web.Files, runtimeBackend())
	if e != nil {
		return e
	}
	app.SetDistribution(distribution)
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
	fmt.Printf("KurumiFRP %s\nOpen http://%s\nNew data directories use an empty administrator password. For existing data, see the installation guide.\n", version.Current, *addr)
	if *open {
		if e := openBrowser(app.BrowserURL()); e != nil {
			log.Print("Browser could not be opened; use the printed local URL.")
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
	app.RequestShutdown()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return server.Shutdown(ctx)
}
