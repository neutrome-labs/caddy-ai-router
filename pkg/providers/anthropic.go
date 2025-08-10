package providers

import (
	"net/http"
	"strings"

	"github.com/neutrome-labs/caddy-ai-router/pkg/common"
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

	common.HookHttpRequestBody(r, func(r *http.Request, body []byte) ([]byte, error) {
		transformedBody, err := transforms.TransformRequestToAnthropic(r, body, modelName, logger)
		if err != nil {
			logger.Error("Failed to transform request body for Anthropic", zap.Error(err))
			return nil, err
		}
		return transformedBody, nil
	})

	r.Header.Set("Content-Type", "application/json")

	// Anthropic specific headers
	r.Header.Set("x-api-key", r.Header.Get("Authorization"))
	// r.Header.Set("anthropic-version", "2023-06-01")
	r.Header.Del("Authorization")

	return nil
}

// ModifyCompletionResponse transforms the Anthropic's response to the unified format.
func (p *AnthropicProvider) ModifyCompletionResponse(r *http.Request, resp *http.Response, logger *zap.Logger) error {
	return common.HookHttpResponseBody(resp, func(resp *http.Response, body []byte) ([]byte, error) {
		return common.HookHttpResponseJsonChunks(func(body []byte) ([]byte, error) {
			return transforms.TransformResponseFromAnthropic(body, logger)
		})(resp, body)
	})
}

// FetchModels is a no-op for Anthropic as they don't have a models API.
func (p *AnthropicProvider) FetchModels(baseURL string, apiKey string, httpClient *http.Client, logger *zap.Logger) ([]map[string]any, error) {
	return nil, nil
}
