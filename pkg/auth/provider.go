package auth

// ExternalAPIKeyProvider defines the interface for a service that can provide
// API keys for upstream providers.
type ExternalAPIKeyProvider interface {
	// GetExternalAPIKey fetches an API key for a given target identifier (e.g., provider name)
	// and an optional user ID (for user-specific keys).
	GetExternalAPIKey(targetIdentifier string, userID string) (string, error)
}
