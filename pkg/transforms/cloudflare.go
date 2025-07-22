package transforms

import (
	"net/http"

	"go.uber.org/zap"
)

// TransformRequestToCloudflareAI is a no-op for the request body, as it's the unified format.
func TransformRequestToCloudflareAI(r *http.Request, originalBody []byte, modelName string, logger *zap.Logger) ([]byte, error) {
	return originalBody, nil
}

// TransformResponseFromCloudflareAI is a no-op for the response body, as it's the unified format.
func TransformResponseFromCloudflareAI(respBody []byte, logger *zap.Logger) ([]byte, error) {
	return respBody, nil
}
