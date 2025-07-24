package transforms

import (
	"encoding/json"
	"net/http"

	"go.uber.org/zap"
)

// TransformRequestToCloudflareAI is a no-op for the request body, as it's the unified format.
func TransformRequestToCloudflareAI(r *http.Request, originalBody []byte, modelName string, logger *zap.Logger) ([]byte, error) {
	// we need to unset model from body since Cloudflare AI expects it in the URL path

	var bodyMap map[string]interface{}
	if err := json.Unmarshal(originalBody, &bodyMap); err != nil {
		logger.Error("Failed to unmarshal request body for Cloudflare AI transformation", zap.Error(err))
		return nil, err
	}

	if _, ok := bodyMap["model"]; ok {
		delete(bodyMap, "model") // Remove model from body as it's in the URL path
	}

	transformedBody, err := json.Marshal(bodyMap)
	if err != nil {
		logger.Error("Failed to marshal transformed request body for Cloudflare AI", zap.Error(err))
		return nil, err
	}

	return transformedBody, nil
}

// TransformResponseFromCloudflareAI is a no-op for the response body, as it's the unified format.
func TransformResponseFromCloudflareAI(respBody []byte, logger *zap.Logger) ([]byte, error) {
	return respBody, nil
}
