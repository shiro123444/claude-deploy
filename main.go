package main

import (
	"flag"
	"fmt"
	"log"
	"os/exec"
	"runtime"

	"claude-relay/internal/config"
	"claude-relay/internal/server"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8787", "listen address")
	noBrowser := flag.Bool("no-browser", false, "don't auto-open browser")
	flag.Parse()

	config.Init()

	srv := server.New(*addr)

	url := "http://" + *addr
	if !*noBrowser {
		openBrowser(url)
	}

	fmt.Printf("claude-relay running at %s\n", url)
	log.Fatal(srv.ListenAndServe())
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return
	}
	cmd.Start()
}
