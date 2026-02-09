package server

import (
	"encoding/json"
	"net/http"

	"claude-relay/internal/config"
	"claude-relay/internal/deployer"
	"claude-relay/internal/models"
	"claude-relay/internal/relay"
)

type apiResponse struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, apiResponse{Status: "error", Message: msg})
}

// --- Config ---

func handleGetConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.Load()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	// Mask API key
	if len(cfg.APIKey) > 10 {
		cfg.APIKey = cfg.APIKey[:7] + "****"
	}
	writeJSON(w, 200, cfg)
}

func handlePutConfig(w http.ResponseWriter, r *http.Request) {
	var cfg models.Config
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeError(w, 400, "invalid JSON: "+err.Error())
		return
	}

	// If masked key sent back, preserve the original
	if len(cfg.APIKey) > 4 && cfg.APIKey[len(cfg.APIKey)-4:] == "****" {
		existing, _ := config.Load()
		if existing != nil {
			cfg.APIKey = existing.APIKey
		}
	}

	if err := config.Save(&cfg); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, apiResponse{Status: "ok"})
}

// --- Models ---

func handleDetectModels(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.Load()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if cfg.BaseURL == "" || cfg.APIKey == "" {
		writeError(w, 400, "base_url and api_key must be configured first")
		return
	}
	result, err := relay.FetchModels(cfg.BaseURL, cfg.APIKey)
	if err != nil {
		writeError(w, 502, err.Error())
		return
	}
	mappings := relay.SuggestMappings(result)
	opus, sonnet, haiku := relay.SuggestDefaults(result)
	writeJSON(w, 200, map[string]any{
		"models":           result,
		"suggest_mappings": mappings,
		"suggest_opus":     opus,
		"suggest_sonnet":   sonnet,
		"suggest_haiku":    haiku,
	})
}

// --- Deploy ---

func handleDeploy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TargetName string `json:"target_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid JSON")
		return
	}

	cfg, err := config.Load()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}

	target := findTarget(cfg, req.TargetName)
	if target == nil {
		writeError(w, 404, "target not found: "+req.TargetName)
		return
	}

	if err := deployer.Deploy(*target, cfg); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, apiResponse{Status: "ok", Message: "deployed to " + req.TargetName})
}

func handleDeployStatus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TargetName string `json:"target_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid JSON")
		return
	}

	cfg, err := config.Load()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}

	target := findTarget(cfg, req.TargetName)
	if target == nil {
		writeError(w, 404, "target not found")
		return
	}

	status, err := deployer.Status(*target)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, status)
}

func handleRestore(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TargetName string `json:"target_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid JSON")
		return
	}

	cfg, err := config.Load()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}

	target := findTarget(cfg, req.TargetName)
	if target == nil {
		writeError(w, 404, "target not found")
		return
	}

	if err := deployer.Restore(*target); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, apiResponse{Status: "ok", Message: "restored " + req.TargetName})
}

// --- Targets ---

func handleGetTargets(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.Load()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, cfg.Targets)
}

func handleAddTarget(w http.ResponseWriter, r *http.Request) {
	var target models.Target
	if err := json.NewDecoder(r.Body).Decode(&target); err != nil {
		writeError(w, 400, "invalid JSON")
		return
	}
	if target.Name == "" || target.Type == "" {
		writeError(w, 400, "name and type are required")
		return
	}

	cfg, err := config.Load()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}

	// Check duplicate
	for _, t := range cfg.Targets {
		if t.Name == target.Name {
			writeError(w, 409, "target already exists: "+target.Name)
			return
		}
	}

	cfg.Targets = append(cfg.Targets, target)
	if err := config.Save(cfg); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, apiResponse{Status: "ok"})
}

func handleDeleteTarget(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "local" {
		writeError(w, 400, "cannot delete local target")
		return
	}

	cfg, err := config.Load()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}

	filtered := cfg.Targets[:0]
	found := false
	for _, t := range cfg.Targets {
		if t.Name == name {
			found = true
			continue
		}
		filtered = append(filtered, t)
	}
	if !found {
		writeError(w, 404, "target not found")
		return
	}

	cfg.Targets = filtered
	if err := config.Save(cfg); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, apiResponse{Status: "ok"})
}

// --- Helpers ---

func findTarget(cfg *models.Config, name string) *models.Target {
	for i := range cfg.Targets {
		if cfg.Targets[i].Name == name {
			return &cfg.Targets[i]
		}
	}
	return nil
}
