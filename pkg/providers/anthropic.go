package providers

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/neutrome-labs/caddy-ai-router/pkg/transforms"
	"go.uber.org/zap"
)

// AnthropicProvider implements the Provider interface for Anthropic.
type AnthropicProvider struct{}

// Name returns the name of the provider.
func (p *AnthropicProvider) Name() string {
	return "anthropic"
}

// ModifyCompletionRequest transforms the incoming request to a format Anthropic understands.
func (p *AnthropicProvider) ModifyCompletionRequest(r *http.Request, modelName string, logger *zap.Logger) error {
	r.URL.Path = strings.TrimRight(r.URL.Path, "/") + "/v1/messages"

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		logger.Error("Failed to read request body for anthropic transformation", zap.Error(err))
		return err
	}
	r.Body.Close()

	transformedBody, err := transforms.TransformRequestToAnthropic(r, bodyBytes, modelName, logger)
	if err != nil {
		return err
	}

	r.Body = io.NopCloser(bytes.NewBuffer(transformedBody))
	r.ContentLength = int64(len(transformedBody))
	r.Header.Set("Content-Type", "application/json")
	// Anthropic specific headers
	r.Header.Set("x-api-key", r.Header.Get("Authorization"))
	// r.Header.Set("anthropic-version", "2023-06-01")
	r.Header.Del("Authorization")
	return nil
}

// ModifyCompletionResponse transforms the Anthropic's response to the unified format.
func (p *AnthropicProvider) ModifyCompletionResponse(w http.ResponseWriter, r *http.Request, resp *http.Response, logger *zap.Logger) error {
	transformedBody, err := transforms.TransformResponseFromAnthropic(resp.Body, logger)
	if err != nil {
		return err
	}
	resp.Body = transformedBody
	resp.Header.Del("Content-Length")
	return nil
}

// FetchModels is a no-op for Anthropic as they don't have a models API.
func (p *AnthropicProvider) FetchModels(baseURL string, apiKey string, httpClient *http.Client, logger *zap.Logger) ([]interface{}, error) {
	return nil, nil
}
