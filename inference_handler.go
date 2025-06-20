package ai_router

import (
	"bytes"
	"context" // For request context
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	// "time" // No longer needed here as transaction timing is moved

	"github.com/caddyserver/caddy/v2/modules/caddyhttp" // Still needed for 'next' if we keep it
	// ai_helpers "github.com/neutrome-labs/caddy-ai-router-helpers" // Import removed
	"go.uber.org/zap"
)

// responseLogger struct and its methods are removed from here.
// It's expected to be handled by AITransactionsMiddleware.

// handlePostInferenceRequest handles POST requests for AI inference.
// It assumes client auth has been validated and user details are in context (if AIKeysMiddleware is used).
// It fetches upstream API keys (if ExternalAPIKeyProvider is available) and proxies the request.
// Transaction logging is handled by a subsequent middleware.
func (cr *AICoreRouter) handlePostInferenceRequest(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler, apiKeyService ExternalAPIKeyProvider) error {
	// Client Authorization header validation is removed (expected to be handled by a preceding middleware like AIKeysMiddleware)

	reqCtx := r.Context() // Get context once

	// Retrieve user details from context using string keys
	userIDVal := reqCtx.Value(UserIDContextKeyString)
	apiKeyIDVal := reqCtx.Value(ApiKeyIDContextKeyString)

	userID, _ := userIDVal.(string)
	apiKeyID, _ := apiKeyIDVal.(string)

	// and userID is not found, it might be an issue.
	if apiKeyService != nil && userID == "" {
		cr.logger.Warn("ExternalAPIKeyProvider service is available, but userID not found in context for POST request. Upstream key fetching might use a generic key or fail if user-specific key is required.",
			zap.String("path", r.URL.Path))
		// Proceeding; GetExternalAPIKey should handle.
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		cr.logger.Error("Failed to read request body for POST", zap.Error(err))
		http.Error(w, "Failed to read request body", http.StatusInternalServerError)
		return err
	}
	r.Body.Close()
	r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes)) // Restore body for proxy

	var requestPayload struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(bodyBytes, &requestPayload); err != nil {
		cr.logger.Error("Failed to parse JSON request body for POST", zap.Error(err), zap.ByteString("body", bodyBytes))
		http.Error(w, "Invalid JSON request body", http.StatusBadRequest)
		return err
	}
	if requestPayload.Model == "" {
		cr.logger.Error("Request body missing 'model' field for POST", zap.ByteString("body", bodyBytes))
		http.Error(w, "'model' field is required in JSON request body", http.StatusBadRequest)
		return fmt.Errorf("'model' field is required")
	}
	cr.logger.Debug("Received AI POST request in core router", zap.String("model_requested", requestPayload.Model))

	providerName, actualModelName := cr.resolveProviderAndModel(requestPayload.Model)
	if providerName == "" {
		cr.logger.Error("Could not determine provider for model (POST)", zap.String("requested_model", requestPayload.Model))
		http.Error(w, fmt.Sprintf("Could not determine provider for model: %s", requestPayload.Model), http.StatusBadRequest)
		return fmt.Errorf("provider not found for model %s", requestPayload.Model)
	}

	cr.mu.RLock()
	provider, ok := cr.Providers[providerName]
	cr.mu.RUnlock()
	if !ok {
		cr.logger.Error("Resolved provider not found in configuration (POST)", zap.String("provider_name", providerName))
		http.Error(w, "Internal server error: provider configuration missing", http.StatusInternalServerError)
		return fmt.Errorf("internal: provider %s not found post-resolution", providerName)
	}

	// Set providerName and actualModelName in context for AITransactionsMiddleware (or other subsequent handlers)
	// reqCtx is already defined
	reqCtx = context.WithValue(reqCtx, ProviderNameContextKeyString, providerName)
	reqCtx = context.WithValue(reqCtx, ActualModelNameContextKeyString, actualModelName)
	r = r.WithContext(reqCtx)

	var upstreamAPIKey string
	if apiKeyService != nil {
		providerTarget := strings.ToLower(provider.Name)
		cr.logger.Debug("Attempting to fetch upstream API key via ExternalAPIKeyProvider (POST)",
			zap.String("provider_target", providerTarget),
			zap.String("user_id", userID))

		fetchedKey, keyErr := apiKeyService.GetExternalAPIKey(providerTarget, userID) // Use generic service and method
		if keyErr != nil {
			cr.logger.Error("Failed to fetch upstream API key from ExternalAPIKeyProvider (POST)",
				zap.Error(keyErr),
				zap.String("provider_target", providerTarget),
				zap.String("user_id", userID))

			if strings.Contains(keyErr.Error(), "not found") || strings.Contains(keyErr.Error(), "forbidden") {
				http.Error(w, "Forbidden: Upstream API credentials not found or access denied.", http.StatusForbidden)
			} else {
				http.Error(w, "Service Unavailable: Could not retrieve API credentials for upstream provider.", http.StatusServiceUnavailable)
			}
			return keyErr
		}
		if fetchedKey == "" {
			cr.logger.Error("No upstream API key found via ExternalAPIKeyProvider for target/user (POST)",
				zap.String("provider_target", providerTarget),
				zap.String("user_id", userID))
			http.Error(w, "Forbidden: Upstream API credentials not found for your account with this provider.", http.StatusForbidden)
			return fmt.Errorf("API key not found via ExternalAPIKeyProvider for target %s, user %s", providerTarget, userID)
		}
		upstreamAPIKey = fetchedKey
		cr.logger.Info("Successfully fetched upstream API key via ExternalAPIKeyProvider for POST",
			zap.String("provider_target", providerTarget))
	} else {
		cr.logger.Info("ExternalAPIKeyProvider not available. Upstream POST request will not have Authorization header set by ai_router unless provider config sets it.")
	}

	cr.logger.Info("Routing POST request in core router",
		zap.String("original_model", requestPayload.Model),
		zap.String("target_provider", provider.Name),
		zap.String("actual_model_for_provider", actualModelName),
		zap.String("target_upstream_base", provider.APIBaseURL),
		zap.String("transformation_style", provider.TransformationStyle),
		zap.String("user_id", userID),      // For logging
		zap.String("api_key_id", apiKeyID), // For logging
	)

	// Prepare request body: transform if style is set, otherwise update model if needed.
	finalBodyBytes := bodyBytes
	// modelInBody := actualModelName // Default to resolved model name - Removed as it's not used

	if provider.TransformationStyle != "" {
		var transformErr error
		switch provider.TransformationStyle {
		case "google_ai_style":
			// For Google, model name is usually in URL, not body.
			// The transformRequestToGoogleAI expects the original body and doesn't use modelName for body content.
			finalBodyBytes, transformErr = transformRequestToGoogleAI(bodyBytes, actualModelName, cr.logger)
		case "anthropic_style":
			finalBodyBytes, transformErr = transformRequestToAnthropic(bodyBytes, actualModelName, cr.logger)
		default:
			cr.logger.Warn("Unknown transformation style, proceeding with original body but updated model name if necessary",
				zap.String("style", provider.TransformationStyle))
			// Fall through to default model update logic if style is unknown
		}
		if transformErr != nil {
			cr.logger.Error("Failed to transform request body",
				zap.Error(transformErr),
				zap.String("style", provider.TransformationStyle))
			http.Error(w, fmt.Sprintf("Failed to transform request for provider style %s", provider.TransformationStyle), http.StatusInternalServerError)
			return transformErr
		}
		// If transformation was successful, modelInBody is handled by the transformation function.
		// For Google, the model is not in the body. For Anthropic, it is.
		// The key is that `finalBodyBytes` is now the correctly transformed payload.
	} else if actualModelName != requestPayload.Model {
		// No transformation style, but model name needs updating in the generic payload
		var fullPayload map[string]interface{}
		if err := json.Unmarshal(bodyBytes, &fullPayload); err != nil {
			cr.logger.Error("Failed to re-parse full JSON request body for model name modification (POST)", zap.Error(err))
			http.Error(w, "Failed to parse JSON for modification", http.StatusInternalServerError)
			return err
		}
		fullPayload["model"] = actualModelName // Update model name
		modifiedBodyBytes, errMarshal := json.Marshal(fullPayload)
		if errMarshal != nil {
			cr.logger.Error("Failed to marshal modified request body with new model name (POST)", zap.Error(errMarshal))
			http.Error(w, "Failed to prepare modified request", http.StatusInternalServerError)
			return errMarshal
		}
		finalBodyBytes = modifiedBodyBytes
		cr.logger.Info("Modified request body with new model name (POST)", zap.String("new_model", actualModelName))
	}

	// Set the final body for the proxy
	r.Body = io.NopCloser(bytes.NewBuffer(finalBodyBytes))
	r.ContentLength = int64(len(finalBodyBytes))
	r.Header.Set("Content-Type", "application/json") // Transformations assume JSON output

	if upstreamAPIKey != "" {
		r.Header.Set("Authorization", "Bearer "+upstreamAPIKey)
		cr.logger.Info("Set Authorization header for upstream POST request")
	}

	provider.proxy.ServeHTTP(w, r) // Directly proxy the request

	return nil // Errors handled by proxy or earlier checks
}
