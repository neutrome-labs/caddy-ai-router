package server

import (
	"strings"

	"go.uber.org/zap"
)

// resolveProviderAndModel determines the provider and actual model name from a requested model string.
// It handles explicit provider prefixes (e.g., "provider#model_name"),
// model-specific defaults, and a super default provider.
func (cr *AICoreRouter) resolveProviderAndModel(requestedModel string) (providerName string, actualModelName string) { // Receiver changed to AICoreRouter (cr)
	cr.mu.RLock() // Ensure read lock for accessing shared provider maps
	defer cr.mu.RUnlock()

	actualModelName = requestedModel // Default to requested model name

	// Check for explicit provider prefix: "providerName/modelName"
	parts := strings.SplitN(requestedModel, "/", 2)
	if len(parts) == 2 {
		pName := strings.ToLower(parts[0])
		model := parts[1]
		if _, ok := cr.Providers[pName]; ok { // Check if the prefixed provider is configured
			cr.logger.Debug("Found explicit provider by prefix", zap.String("prefix", pName), zap.String("model", model)) // Changed to Debug
			return pName, model
		}
		// Log if prefix is found but provider isn't recognized, then proceed to other checks
		cr.logger.Debug("Prefix found but provider not recognized, checking defaults", zap.String("prefix", pName), zap.String("requested_model", requestedModel)) // Changed to Debug
	}

	// Check for model-specific default provider
	if pName, ok := cr.DefaultProviderForModel[requestedModel]; ok {
		if _, providerExists := cr.Providers[pName]; providerExists {
			cr.logger.Debug("Found default provider for model", zap.String("model", requestedModel), zap.String("provider", pName)) // Changed to Debug
			return pName, requestedModel                                                                                            // Model name remains as requested
		}
		cr.logger.Warn("Default provider for model configured but provider itself not found", zap.String("model", requestedModel), zap.String("configured_provider", pName))
	}

	// Use super default provider if no other match
	if cr.SuperDefaultProvider != "" {
		if _, ok := cr.Providers[cr.SuperDefaultProvider]; ok {
			cr.logger.Debug("Using super default provider", zap.String("provider", cr.SuperDefaultProvider), zap.String("model", requestedModel)) // Changed to Debug
			return cr.SuperDefaultProvider, requestedModel                                                                                        // Model name remains as requested
		}
		// This case should ideally be caught during Provision/Validate, but good to log
		cr.logger.Error("Super default provider configured but not found in providers list during resolution", zap.String("super_default_provider", cr.SuperDefaultProvider))
	}

	// If no provider could be resolved
	cr.logger.Warn("Could not resolve provider for model", zap.String("model", requestedModel))
	return "", requestedModel // Return empty provider name, model name as is
}

// SingleJoiningSlash is a helper from net/http/httputil to join URL paths.
// It ensures that there's exactly one slash between a and b.
func SingleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		if b == "" { // Avoid adding slash if b is empty, or if a is empty and b is not a path
			return a
		}
		return a + "/" + b // Add a slash if neither has one and b is not empty
	}
	return a + b // One has a slash, the other doesn't, or b is empty
}
