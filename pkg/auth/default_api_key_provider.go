package auth

import (
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"
)

// DefaultEnvAPIKeyProvider implements the ExternalAPIKeyProvider interface
// by fetching API keys from environment variables.
// It expects environment variables in the format: TARGETIDENTIFIER_API_KEY
// For example, for targetIdentifier "openai", it looks for "OPENAI_API_KEY".
type DefaultEnvAPIKeyProvider struct {
	logger *zap.Logger
}

// NewDefaultEnvAPIKeyProvider creates a new instance of DefaultEnvAPIKeyProvider.
// It requires a logger for its operations.
func NewDefaultEnvAPIKeyProvider(logger *zap.Logger) *DefaultEnvAPIKeyProvider {
	if logger == nil {
		// Fallback to a no-op logger if none is provided, though ideally a logger should always be passed.
		logger = zap.NewNop()
	}
	return &DefaultEnvAPIKeyProvider{logger: logger}
}

// GetExternalAPIKey fetches an API key for a given target identifier from environment variables.
// The userID parameter is ignored by this provider as keys are not user-specific.
func (p *DefaultEnvAPIKeyProvider) GetExternalAPIKey(targetIdentifier string, userID string) (string, error) {
	if targetIdentifier == "" {
		p.logger.Error("Target identifier cannot be empty for DefaultEnvAPIKeyProvider")
		return "", fmt.Errorf("target identifier cannot be empty")
	}

	// Construct the environment variable name, e.g., "OPENAI_API_KEY" from "openai"
	envVarName := strings.ToUpper(targetIdentifier) + "_API_KEY"

	apiKey := os.Getenv(envVarName)

	if apiKey == "" {
		p.logger.Warn("API key not found in environment variable",
			zap.String("env_var_name", envVarName),
			zap.String("target_identifier", targetIdentifier),
			zap.String("user_id_ignored", userID))
		// It's important to distinguish between "not found" and an actual error.
		// Returning an error that indicates "not found" allows the caller to decide if this is fatal.
		return "", fmt.Errorf("API key not found in environment variable %s for target %s", envVarName, targetIdentifier)
	}

	p.logger.Info("Successfully retrieved API key from environment variable",
		zap.String("env_var_name", envVarName),
		zap.String("target_identifier", targetIdentifier))
	return apiKey, nil
}
