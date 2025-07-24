package providers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/neutrome-labs/caddy-ai-router/pkg/common"
	"github.com/neutrome-labs/caddy-ai-router/pkg/transforms"
	"go.uber.org/zap"
)

// CloudflareProvider implements the Provider interface for Cloudflare.
type CloudflareProvider struct{}

// Name returns the name of the provider.
func (p *CloudflareProvider) Name() string {
	return "cloudflare"
}

// ModifyCompletionRequest sets the URL path for the completion request.
func (p *CloudflareProvider) ModifyCompletionRequest(r *http.Request, modelName string, logger *zap.Logger) error {
	r.URL.Path = strings.TrimRight(r.URL.Path, "/") + "/run/" + modelName

	common.HookHttpRequestBody(r, func(r *http.Request, body []byte) ([]byte, error) {
		transformedBody, err := transforms.TransformRequestToCloudflareAI(r, body, modelName, logger)
		if err != nil {
			logger.Error("Failed to transform request body for Cloudflare AI", zap.Error(err))
			return nil, err
		}
		return transformedBody, nil
	})

	return nil
}

// ModifyCompletionResponse is a no-op for Cloudflare.
func (p *CloudflareProvider) ModifyCompletionResponse(w http.ResponseWriter, r *http.Request, resp *http.Response, logger *zap.Logger) error {
	return nil
}

// FetchModels fetches the models from the Cloudflare API.
func (p *CloudflareProvider) FetchModels(baseURL string, apiKey string, httpClient *http.Client, logger *zap.Logger) ([]interface{}, error) {
	modelsURL := strings.TrimRight(baseURL, "/") + "/models/search"
	req, err := http.NewRequest(http.MethodGet, modelsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request for %s: %w", modelsURL, err)
	}
	req.Header.Set("User-Agent", "Caddy-AI-Router")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request to %s failed: %w", modelsURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("request to %s returned status %d: %s", modelsURL, resp.StatusCode, string(bodyBytes))
	}

	var providerResp struct {
		Result []interface{} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&providerResp); err != nil {
		return nil, fmt.Errorf("failed to decode response from %s: %w", modelsURL, err)
	}

	var models []interface{}
	for _, model := range providerResp.Result {
		if m, ok := model.(map[string]interface{}); ok {
			if name, ok := m["name"].(string); ok {
				models = append(models, map[string]interface{}{
					"id":   name,
					"name": name,
				})
			}
		}
	}

	return models, nil
}
