package providers

import (
	"net/http"

	"go.uber.org/zap"
)

// Provider defines the interface for an AI provider.
type Provider interface {
	// Name returns the name of the provider.
	Name() string
	// ModifyCompletionRequest transforms the incoming request to a format the provider understands.
	ModifyCompletionRequest(r *http.Request, modelName string, logger *zap.Logger) error
	// ModifyCompletionResponse transforms the provider's response to the unified format.
	ModifyCompletionResponse(r *http.Request, resp *http.Response, logger *zap.Logger) error
	// FetchModels fetches the models from the provider.
	FetchModels(baseURL string, apiKey string, httpClient *http.Client, logger *zap.Logger) ([]map[string]any, error)
}
