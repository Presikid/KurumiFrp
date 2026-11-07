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
	addr := flag.String("listen", "127.0.0.1:9000", "loopback management listener")
	token := flag.Bool("show-token", false, "print existing local access token and exit")
	flag.Parse()
	abs, e := filepath.Abs(*dir)
	if e != nil {
		return e
	}
	if *token {
		b, e := os.ReadFile(filepath.Join(abs, "access.token"))
		if e != nil {
			return e
		}
		fmt.Println(string(b))
		return nil
	}
	if e = os.MkdirAll(abs, 0700); e != nil {
		return e
	}
	lock := filepath.Join(abs, "manager.lock")
	f, e := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return fmt.Errorf("data directory locked; verify no old manager/frp process is alive before removing manager.lock")
	}
	fmt.Fprintln(f, os.Getpid())
	f.Close()
	defer os.Remove(lock)
	app, e := manager.New(abs, *bin, *addr, web.Files)
	if e != nil {
		return e
	}
	defer app.Close()
	listener, e := net.Listen("tcp", *addr)
	if e != nil {
		return e
	}
	defer listener.Close()
	server := manager.HTTPServer(*addr, app)
	stopped := make(chan os.Signal, 1)
	signal.Notify(stopped, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stopped)
	errs := make(chan error, 1)
	go func() { errs <- server.Serve(listener) }()
	fmt.Printf("KurumiFRP 0.1.0\nOpen http://%s\nGet local token in another terminal: kurumi --data %q --show-token\n", *addr, abs)
	select {
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
