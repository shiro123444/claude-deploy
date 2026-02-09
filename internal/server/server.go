package server

import (
	"io/fs"
	"net/http"

	"claude-relay/frontend"
)

func New(addr string) *http.Server {
	mux := http.NewServeMux()

	// API routes
	mux.HandleFunc("GET /api/config", handleGetConfig)
	mux.HandleFunc("PUT /api/config", handlePutConfig)
	mux.HandleFunc("GET /api/models/detect", handleDetectModels)
	mux.HandleFunc("POST /api/deploy", handleDeploy)
	mux.HandleFunc("POST /api/deploy/status", handleDeployStatus)
	mux.HandleFunc("POST /api/deploy/restore", handleRestore)
	mux.HandleFunc("GET /api/targets", handleGetTargets)
	mux.HandleFunc("POST /api/targets", handleAddTarget)
	mux.HandleFunc("DELETE /api/targets/{name}", handleDeleteTarget)

	// Frontend (embedded)
	frontendFS, _ := fs.Sub(frontend.Assets, ".")
	mux.Handle("/", http.FileServer(http.FS(frontendFS)))

	return &http.Server{Addr: addr, Handler: mux}
}
