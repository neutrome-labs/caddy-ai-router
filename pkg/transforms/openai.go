package transforms

import (
	"encoding/json"
	"net/http"

	"go.uber.org/zap"
)

// TransformRequestToOpenAI is a no-op for the request body, as it's the unified format.
func TransformRequestToOpenAI(r *http.Request, originalBody []byte, modelName string, logger *zap.Logger) ([]byte, error) {
	var bodyMap map[string]any
	if err := json.Unmarshal(originalBody, &bodyMap); err != nil {
		logger.Error("Failed to unmarshal request body for OpenAI transformation", zap.Error(err))
		return nil, err
	}

	if _, ok := bodyMap["model"]; ok {
		bodyMap["model"] = modelName // Ensure the model name is set correctly
	}

	transformedBody, err := json.Marshal(bodyMap)
	if err != nil {
		logger.Error("Failed to marshal transformed request body for OpenAI", zap.Error(err))
		return nil, err
	}

	return transformedBody, nil
}

// TransformResponseFromOpenAI is a no-op for the response body, as it's the unified format.
func TransformResponseFromOpenAI(respBody []byte, logger *zap.Logger) ([]byte, error) {
	return respBody, nil
}
