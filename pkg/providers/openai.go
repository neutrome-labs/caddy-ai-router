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

// OpenAIProvider implements the Provider interface for OpenAI.
type OpenAIProvider struct{}

// Name returns the name of the provider.
func (p *OpenAIProvider) Name() string {
	return "openai"
}

// ModifyCompletionRequest sets the URL path for the completion request.
func (p *OpenAIProvider) ModifyCompletionRequest(r *http.Request, modelName string, logger *zap.Logger) error {
	r.URL.Path = strings.TrimRight(r.URL.Path, "/") + "/chat/completions"

	common.HookHttpRequestBody(r, func(r *http.Request, body []byte) ([]byte, error) {
		transformedBody, err := transforms.TransformRequestToOpenAI(r, body, modelName, logger)
		if err != nil {
			logger.Error("Failed to transform request body for OpenAI", zap.Error(err))
			return nil, err
		}
		return transformedBody, nil
	})

	return nil
}

// ModifyCompletionResponse is a no-op for OpenAI.
func (p *OpenAIProvider) ModifyCompletionResponse(r *http.Request, resp *http.Response, logger *zap.Logger) error {
	return nil
}

// FetchModels fetches the models from the OpenAI API.
func (p *OpenAIProvider) FetchModels(baseURL string, apiKey string, httpClient *http.Client, logger *zap.Logger) ([]map[string]any, error) {
	modelsURL := strings.TrimRight(baseURL, "/") + "/models"
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
		Data []map[string]any `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&providerResp); err != nil {
		return nil, fmt.Errorf("failed to decode response from %s: %w", modelsURL, err)
	}
	return providerResp.Data, nil
}
