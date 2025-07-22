package transforms

import (
	"net/http"

	"go.uber.org/zap"
)

// TransformRequestToOpenAI is a no-op for the request body, as it's the unified format.
func TransformRequestToOpenAI(r *http.Request, originalBody []byte, modelName string, logger *zap.Logger) ([]byte, error) {
	return originalBody, nil
}

// TransformResponseFromOpenAI is a no-op for the response body, as it's the unified format.
func TransformResponseFromOpenAI(respBody []byte, logger *zap.Logger) ([]byte, error) {
	return respBody, nil
}
