package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/neutrome-labs/caddy-ai-router/pkg/auth"
	"go.uber.org/zap"
)

// ModelPricingInfo defines the pricing structure for a model.
type ModelPricingInfo struct {
	Prompt            string `json:"prompt"`
	Completion        string `json:"completion"`
	Request           string `json:"request"`
	Image             string `json:"image"`
	WebSearch         string `json:"web_search,omitempty"`         // Optional
	InternalReasoning string `json:"internal_reasoning,omitempty"` // Optional
	InputCacheRead    string `json:"input_cache_read,omitempty"`   // Optional
	InputCacheWrite   string `json:"input_cache_write,omitempty"`  // Optional
}

// ModelArchitectureInfo defines the architecture of a model.
type ModelArchitectureInfo struct {
	Modality         string   `json:"modality"`
	InputModalities  []string `json:"input_modalities"`
	OutputModalities []string `json:"output_modalities"`
	Tokenizer        string   `json:"tokenizer"`
	InstructType     *string  `json:"instruct_type"` // Pointer for nullable string
}

// ModelTopProviderDetails defines details from the top provider for the model.
type ModelTopProviderDetails struct {
	ContextLength       int  `json:"context_length"`
	MaxCompletionTokens *int `json:"max_completion_tokens"` // Pointer for nullable int
	IsModerated         bool `json:"is_moderated"`
}

// ModelInfo represents a single AI model's details.
type ModelInfo struct {
	ID            string `json:"id"`
	CanonicalSlug string `json:"canonical_slug"`
	// HuggingFaceID string                `json:"hugging_face_id,omitempty"` // Optional
	Name          string                `json:"name"`
	Created       int64                 `json:"created"` // Assuming Unix timestamp
	Description   string                `json:"description"`
	ContextLength int                   `json:"context_length"`
	Architecture  ModelArchitectureInfo `json:"architecture"`
	// Pricing             ModelPricingInfo        `json:"pricing"`
	// TopProvider         ModelTopProviderDetails `json:"top_provider"`
	// PerRequestLimits    any             `json:"per_request_limits"`             // Can be null or an object, use any
	SupportedParameters []string `json:"supported_parameters,omitempty"` // Optional
}

// ProviderModelsResponse is the expected response structure from a provider's /models endpoint.
type ProviderModelsResponse struct {
	Data []ModelInfo `json:"data"`
}

// AggregatedModelsResponse is the structure for the aggregated /models endpoint.
type AggregatedModelsResponse struct {
	Data []ModelInfo `json:"data"`
}

// providerModelResult is a helper struct for concurrent fetching of models from a provider.
type providerModelResult struct {
	providerName string
	models       []ModelInfo
	err          error
}

// handleGetManagedModels handles GET requests to /models.
func (cr *AICoreRouter) handleGetManagedModels(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler, apiKeyService auth.ExternalAPIKeyProvider) error {
	cr.mu.RLock()
	providerConfigs := make([]*ProviderConfig, 0, len(cr.Providers))
	for _, pCfg := range cr.Providers {
		providerConfigs = append(providerConfigs, pCfg)
	}
	cr.mu.RUnlock()

	if len(providerConfigs) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(AggregatedModelsResponse{Data: []ModelInfo{}})
		return nil
	}

	var wg sync.WaitGroup
	resultsChan := make(chan providerModelResult, len(providerConfigs))

	for _, pCfg := range providerConfigs {
		wg.Add(1)
		go func(providerConfig *ProviderConfig) {
			defer wg.Done()
			var apiKey string
			if apiKeyService != nil {
				fetchedKey, err := apiKeyService.GetExternalAPIKey(providerConfig.Name, "")
				if err != nil {
					cr.logger.Warn("Failed to get API key for provider", zap.String("provider", providerConfig.Name), zap.Error(err))
				} else {
					apiKey = fetchedKey
				}
			}

			if providerConfig.Provider == nil {
				resultsChan <- providerModelResult{providerName: providerConfig.Name, err: fmt.Errorf("provider not initialized")}
				return
			}

			models, err := providerConfig.Provider.FetchModels(providerConfig.APIBaseURL, apiKey, cr.httpClient, cr.logger)
			if err != nil {
				resultsChan <- providerModelResult{providerName: providerConfig.Name, err: err}
				return
			}

			var modelInfos []ModelInfo
			for _, model := range models {
				var id string
				var name string
				if modelID, ok := model["id"].(string); ok {
					id = modelID
				} else {
					cr.logger.Warn("Model ID is not a string", zap.Any("model", model), zap.String("provider", providerConfig.Name))
					continue
				}

				if modelName, ok := model["name"].(string); ok {
					name = modelName
				} else {
					cr.logger.Warn("Model name is not a string", zap.Any("model", model), zap.String("provider", providerConfig.Name))
					name = id // Fallback to ID if name is not available
				}

				modelInfo := ModelInfo{
					ID:   id,
					Name: name,
				}
				modelInfos = append(modelInfos, modelInfo)
			}

			resultsChan <- providerModelResult{providerName: providerConfig.Name, models: modelInfos}
		}(pCfg)
	}

	wg.Wait()
	close(resultsChan)

	allModels := []ModelInfo{}
	uniqueModelIDs := make(map[string]bool)

	for result := range resultsChan {
		if result.err != nil {
			cr.logger.Error("Failed to fetch models from provider", zap.String("provider", result.providerName), zap.Error(result.err))
			continue
		}
		for _, model := range result.models {
			if _, exists := uniqueModelIDs[model.ID]; !exists {
				allModels = append(allModels, model)
				uniqueModelIDs[model.ID] = true
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(AggregatedModelsResponse{Data: allModels})

	return nil
}
