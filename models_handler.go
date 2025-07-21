package ai_router

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

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
	// PerRequestLimits    interface{}             `json:"per_request_limits"`             // Can be null or an object, use interface{}
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
// apiKeyService is passed from AICoreRouter.ServeHTTP and can be nil.
func (cr *AICoreRouter) handleGetManagedModels(w http.ResponseWriter, r *http.Request, apiKeyService ExternalAPIKeyProvider) error {
	cr.mu.RLock()
	providerConfigs := make([]*ProviderConfig, 0, len(cr.Providers))
	for _, pCfg := range cr.Providers {
		providerConfigs = append(providerConfigs, pCfg)
	}
	cr.mu.RUnlock()

	if len(providerConfigs) == 0 {
		cr.logger.Info("No providers configured for /models endpoint.")
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
			// var keyErr error // keyErr is declared inside the if block now

			if apiKeyService != nil {
				pbTarget := strings.ToLower(providerConfig.Name) // pbTarget is a bit of a misnomer now, should be targetIdentifier
				cr.logger.Debug("Attempting to fetch API key for /models endpoint via ExternalAPIKeyProvider",
					zap.String("provider", providerConfig.Name),
					zap.String("target_identifier", pbTarget))

				// Use empty string for userID to fetch a general/default key for the target
				fetchedKey, keyErr := apiKeyService.GetExternalAPIKey(pbTarget, "") // Use generic service and method
				if keyErr != nil {
					cr.logger.Warn("Failed to get API key for provider's /models endpoint via ExternalAPIKeyProvider, proceeding without auth",
						zap.String("provider", providerConfig.Name),
						zap.String("target_identifier", pbTarget),
						zap.Error(keyErr))
					// Continue without API key; some /models endpoints might be public.
				} else if fetchedKey != "" {
					apiKey = fetchedKey
					cr.logger.Info("Successfully fetched API key for provider's /models endpoint via ExternalAPIKeyProvider",
						zap.String("provider", providerConfig.Name),
						zap.String("target_identifier", pbTarget))
				} else {
					cr.logger.Info("No API key returned from ExternalAPIKeyProvider for /models endpoint (key might be optional or not found)",
						zap.String("provider", providerConfig.Name),
						zap.String("target_identifier", pbTarget))
				}
			} else {
				cr.logger.Info("ExternalAPIKeyProvider not available for /models, proceeding without attempting API key fetch.", zap.String("provider", providerConfig.Name))
			}

			modelsURL := strings.TrimRight(providerConfig.APIBaseURL, "/") + "/models"
			req, err := http.NewRequest(http.MethodGet, modelsURL, nil)
			if err != nil {
				resultsChan <- providerModelResult{providerName: providerConfig.Name, err: fmt.Errorf("failed to create request for %s: %w", modelsURL, err)}
				return
			}
			req.Header.Set("User-Agent", "Caddy-AI-Gateway/ManagedModels")
			if apiKey != "" {
				req.Header.Set("Authorization", "Bearer "+apiKey)
			}

			resp, err := cr.httpClient.Do(req) // Use cr.httpClient
			if err != nil {
				resultsChan <- providerModelResult{providerName: providerConfig.Name, err: fmt.Errorf("request to %s failed: %w", modelsURL, err)}
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				bodyBytes, _ := io.ReadAll(resp.Body)
				resultsChan <- providerModelResult{providerName: providerConfig.Name, err: fmt.Errorf("request to %s returned status %d: %s", modelsURL, resp.StatusCode, string(bodyBytes))}
				return
			}

			var providerResp ProviderModelsResponse
			if err := json.NewDecoder(resp.Body).Decode(&providerResp); err != nil {
				resultsChan <- providerModelResult{providerName: providerConfig.Name, err: fmt.Errorf("failed to decode response from %s: %w", modelsURL, err)}
				return
			}
			resultsChan <- providerModelResult{providerName: providerConfig.Name, models: providerResp.Data}
		}(pCfg)
	}

	wg.Wait()
	close(resultsChan)

	allModels := []ModelInfo{}
	uniqueModelIDs := make(map[string]bool)

	for result := range resultsChan {
		if result.err != nil {
			cr.logger.Error("Failed to fetch models from provider", // Use cr.logger
				zap.String("provider", result.providerName),
				zap.Error(result.err),
			)
			continue
		}
		for _, model := range result.models {
			if _, exists := uniqueModelIDs[model.ID]; !exists {
				allModels = append(allModels, model)
				uniqueModelIDs[model.ID] = true
			}
		}
	}

	cr.logger.Info("Aggregated models from providers", zap.Int("total_unique_models", len(allModels))) // Use cr.logger

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(AggregatedModelsResponse{Data: allModels}); err != nil {
		cr.logger.Error("Failed to encode aggregated models response", zap.Error(err)) // Use cr.logger
		// Hard to send an error to client at this point if headers already sent
		return err
	}
	return nil
}
