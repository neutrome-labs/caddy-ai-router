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

// GoogleProvider implements the Provider interface for Google AI.
type GoogleProvider struct{}

// Name returns the name of the provider.
func (p *GoogleProvider) Name() string {
	return "google"
}

// ModifyCompletionRequest transforms the incoming request to a format Google AI understands.
func (p *GoogleProvider) ModifyCompletionRequest(r *http.Request, modelName string, logger *zap.Logger) error {
	r.URL.Path = strings.TrimRight(r.URL.Path, "/") + "/models/" + modelName + ":generateContent"

	common.HookHttpRequestBody(r, func(r *http.Request, body []byte) ([]byte, error) {
		transformedBody, err := transforms.TransformRequestToGoogleAI(r, body, modelName, logger)
		if err != nil {
			logger.Error("Failed to transform request body for Google AI", zap.Error(err))
			return nil, err
		}
		return transformedBody, nil
	})

	r.Header.Set("Content-Type", "application/json")
	return nil
}

// ModifyCompletionResponse transforms the Google AI's response to the unified format.
func (p *GoogleProvider) ModifyCompletionResponse(w http.ResponseWriter, r *http.Request, resp *http.Response, logger *zap.Logger) error {
	transformedBody, err := transforms.TransformResponseFromGoogleAI(resp.Body, logger)
	if err != nil {
		return err
	}
	resp.Body = transformedBody
	resp.Header.Del("Content-Length")
	return nil
}

// FetchModels fetches the models from the Google AI API.
func (p *GoogleProvider) FetchModels(baseURL string, apiKey string, httpClient *http.Client, logger *zap.Logger) ([]interface{}, error) {
	modelsURL := strings.TrimRight(baseURL, "/") + "/v1beta/models"
	req, err := http.NewRequest(http.MethodGet, modelsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request for %s: %w", modelsURL, err)
	}
	req.Header.Set("User-Agent", "Caddy-AI-Router")
	if apiKey != "" {
		q := req.URL.Query()
		q.Set("key", apiKey)
		req.URL.RawQuery = q.Encode()
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
		Models []interface{} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&providerResp); err != nil {
		return nil, fmt.Errorf("failed to decode response from %s: %w", modelsURL, err)
	}
	return providerResp.Models, nil
}
