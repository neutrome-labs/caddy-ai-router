package server

import (
	"bytes"
	"context" // For request context
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/caddyserver/caddy/v2/modules/caddyhttp" // Still needed for 'next' if we keep it
	"github.com/neutrome-labs/caddy-ai-router/pkg/auth"
	"github.com/neutrome-labs/caddy-ai-router/pkg/common"
	"go.uber.org/zap"
)

// responseLogger struct and its methods are removed from here.
// It's expected to be handled by AITransactionsMiddleware.

// handlePostInferenceRequest handles POST requests for AI inference.
// It assumes client auth has been validated and user details are in context (if AIKeysMiddleware is used).
// It fetches upstream API keys (if ExternalAPIKeyProvider is available) and proxies the request.
// Transaction logging is handled by a subsequent middleware.
func (cr *AICoreRouter) handlePostInferenceRequest(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler, apiKeyService auth.ExternalAPIKeyProvider) error {
	reqCtx := r.Context()

	userIDVal := reqCtx.Value(UserIDContextKeyString)
	apiKeyIDVal := reqCtx.Value(ApiKeyIDContextKeyString)
	userID, _ := userIDVal.(string)
	apiKeyID, _ := apiKeyIDVal.(string)

	if apiKeyService != nil && userID == "" {
		cr.logger.Warn("ExternalAPIKeyProvider service is available, but userID not found in context for POST request.", zap.String("path", r.URL.Path))
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		cr.logger.Error("Failed to read request body for POST", zap.Error(err))
		http.Error(w, "Failed to read request body", http.StatusInternalServerError)
		return err
	}
	r.Body.Close()
	r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	var requestPayload struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(bodyBytes, &requestPayload); err != nil {
		cr.logger.Error("Failed to parse JSON request body for POST", zap.Error(err), zap.ByteString("body", bodyBytes))
		http.Error(w, "Invalid JSON request body", http.StatusBadRequest)
		return err
	}
	if requestPayload.Model == "" {
		http.Error(w, "'model' field is required in JSON request body", http.StatusBadRequest)
		return fmt.Errorf("'model' field is required")
	}

	providerName, actualModelName := cr.resolveProviderAndModel(requestPayload.Model)
	if providerName == "" {
		http.Error(w, fmt.Sprintf("Could not determine provider for model: %s", requestPayload.Model), http.StatusBadRequest)
		return fmt.Errorf("provider not found for model %s", requestPayload.Model)
	}

	cr.mu.RLock()
	providerConfig, ok := cr.Providers[providerName]
	cr.mu.RUnlock()
	if !ok {
		http.Error(w, "Internal server error: provider configuration missing", http.StatusInternalServerError)
		return fmt.Errorf("internal: provider %s not found post-resolution", providerName)
	}

	reqCtx = context.WithValue(reqCtx, ProviderNameContextKeyString, providerName)
	reqCtx = context.WithValue(reqCtx, ActualModelNameContextKeyString, actualModelName)
	r = r.WithContext(reqCtx)

	if apiKeyService != nil {
		providerTarget := strings.ToLower(providerConfig.Name)
		fetchedKey, keyErr := apiKeyService.GetExternalAPIKey(providerTarget, userID)
		if keyErr != nil {
			cr.logger.Error("Failed to fetch upstream API key", zap.Error(keyErr), zap.String("provider", providerTarget))
			http.Error(w, "Service Unavailable: Could not retrieve API credentials.", http.StatusServiceUnavailable)
			return keyErr
		}
		if fetchedKey == "" {
			http.Error(w, "Forbidden: Upstream API credentials not found.", http.StatusForbidden)
			return fmt.Errorf("API key not found for target %s", providerTarget)
		}
		r.Header.Set("Authorization", "Bearer "+fetchedKey)
	}

	cr.logger.Info("Routing POST request",
		zap.String("original_model", requestPayload.Model),
		zap.String("provider", providerConfig.Name),
		zap.String("actual_model", actualModelName),
		zap.String("user_id", userID),
		zap.String("api_key_id", apiKeyID),
	)

	common.FireObservabilityEvent(userID, "inference-start", map[string]interface{}{
		"model":      requestPayload.Model,
		"user_id":    userID,
		"api_key_id": apiKeyID,
	})

	start_time := common.CaddyClock.Now()
	defer func() {
		common.FireObservabilityEvent(userID, "inference-stop", map[string]interface{}{
			"model":       requestPayload.Model,
			"duration_ms": common.CaddyClock.Now().Sub(start_time).Milliseconds(),
			"user_id":     userID,
			"api_key_id":  apiKeyID,
		})
	}()

	providerConfig.proxy.ServeHTTP(w, r)

	return nil
}
