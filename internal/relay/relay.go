package relay

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"claude-relay/internal/models"
)

type modelsResponse struct {
	Data []models.RelayModel `json:"data"`
}

// FetchModels queries the relay's /v1/models endpoint.
func FetchModels(baseURL, apiKey string) ([]models.RelayModel, error) {
	base := strings.TrimRight(baseURL, "/")
	// Avoid double /v1 if base_url already ends with /v1
	url := base + "/models"
	if !strings.HasSuffix(base, "/v1") {
		url = base + "/v1/models"
	}

	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("relay unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("relay returned HTTP %d", resp.StatusCode)
	}

	var result modelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	// Sort by ID for consistent display
	sort.Slice(result.Data, func(i, j int) bool {
		return result.Data[i].ID < result.Data[j].ID
	})

	return result.Data, nil
}

// SuggestMappings generates recommended model mappings from detected models.
func SuggestMappings(available []models.RelayModel) []models.ModelMapping {
	ids := make([]string, len(available))
	for i, m := range available {
		ids[i] = m.ID
	}

	var mappings []models.ModelMapping

	// Find best main model (opus)
	if best := findBest(ids, []string{"thinking", "opus-4-6", "opus-3-5", "opus"}); best != "" {
		mappings = append(mappings, models.ModelMapping{VSCodeID: "claude-opus-4.6", RelayID: best})
	}

	// Find best sonnet
	if best := findBest(ids, []string{"sonnet-4-5", "sonnet-4", "sonnet-3-5", "sonnet"}); best != "" {
		mappings = append(mappings, models.ModelMapping{VSCodeID: "claude-sonnet-4.5", RelayID: best})
	}

	// Find best haiku
	if best := findBest(ids, []string{"haiku-4-5", "haiku-3-5", "haiku"}); best != "" {
		mappings = append(mappings, models.ModelMapping{VSCodeID: "claude-haiku-4.5", RelayID: best})
	}

	return mappings
}

// SuggestDefaults returns recommended default models for the 3 tiers.
func SuggestDefaults(available []models.RelayModel) (opus, sonnet, haiku string) {
	ids := make([]string, len(available))
	for i, m := range available {
		ids[i] = m.ID
	}
	opus = findBest(ids, []string{"opus-4-6", "opus-4-5", "opus"})
	sonnet = findBest(ids, []string{"sonnet-4-5", "sonnet-4", "sonnet-3-5", "sonnet"})
	haiku = findBest(ids, []string{"haiku-4-5", "haiku-3-5", "haiku"})
	return
}

func findBest(ids []string, keywords []string) string {
	for _, kw := range keywords {
		var matches []string
		for _, id := range ids {
			if strings.Contains(id, kw) {
				matches = append(matches, id)
			}
		}
		if len(matches) > 0 {
			// Prefer longer names (more specific), and prefer "thinking" variants
			sort.Slice(matches, func(i, j int) bool {
				iThink := strings.Contains(matches[i], "thinking")
				jThink := strings.Contains(matches[j], "thinking")
				if iThink != jThink {
					return iThink
				}
				return len(matches[i]) > len(matches[j])
			})
			return matches[0]
		}
	}
	return ""
}
