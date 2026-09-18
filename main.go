package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/jaydip216/db0/internal/server"
	appstate "github.com/jaydip216/db0/internal/state"
)

//go:embed web/dist
var webAssets embed.FS

func main() {
	noOpen := flag.Bool("no-open", false, "do not open the browser")
	port := flag.Int("port", 0, "loopback port (0 chooses a free port)")
	flag.Parse()

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		log.Fatal(err)
	}
	token, err := randomToken()
	if err != nil {
		log.Fatal(err)
	}
	dist, err := fs.Sub(webAssets, "web/dist")
	if err != nil {
		log.Fatal(err)
	}

	persistent := openState()
	app := server.NewWithState(token, dist, persistent)
	defer app.Close()
	url := fmt.Sprintf("http://%s/#token=%s", ln.Addr().String(), token)
	fmt.Printf("db0 listening at %s\n", url)
	if !*noOpen {
		if err := openBrowser(url); err != nil {
			log.Printf("could not open browser: %v", err)
		}
	}
	httpServer := &http.Server{Handler: app, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 2 * time.Minute}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(ctx)
	}()
	if err := httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func openState() *appstate.Store {
	configDir, err := os.UserConfigDir()
	if err != nil {
		log.Printf("persistent profiles and history disabled: %v", err)
		return appstate.NewMemory()
	}
	persistent, err := appstate.Open(filepath.Join(configDir, "db0", "state.json"))
	if err != nil {
		log.Printf("persistent profiles and history disabled: %v", err)
		return appstate.NewMemory()
	}
	return persistent
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Start()
}
