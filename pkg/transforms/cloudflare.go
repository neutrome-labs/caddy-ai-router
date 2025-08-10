package transforms

import (
	"encoding/json"
	"net/http"

	"go.uber.org/zap"
)

// TransformRequestToCloudflareAI is a no-op for the request body, as it's the unified format.
func TransformRequestToCloudflareAI(r *http.Request, originalBody []byte, modelName string, logger *zap.Logger) ([]byte, error) {
	// we need to unset model from body since Cloudflare AI expects it in the URL path

	var bodyMap map[string]any
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
	var respBodyJson map[string]any
	if err := json.Unmarshal(respBody, &respBodyJson); err != nil {
		logger.Error("Failed to decode response body for Cloudflare AI", zap.Error(err))
		return respBody, err
	}

	responseText, ok := respBodyJson["response"].(string)
	if !ok {
		_, ok := respBodyJson["result"].(map[string]interface{})
		if ok {
			responseText, ok = respBodyJson["result"].(map[string]interface{})["response"].(string)
			if !ok {
				return respBody, nil // If no response text, return original body
			}
		}
	}

	// Map Cloudflare's response format to the default format
	defaultResp := map[string]any{
		"choices": []map[string]any{
			{
				"message": map[string]any{
					"role":    "assistant",
					"content": responseText,
				},
				"index":         0,
				"logprobs":      nil,
				"finish_reason": "",
			},
		},
	}

	newRespBody, err := json.Marshal(defaultResp)
	if err != nil {
		logger.Error("Failed to marshal response body for Cloudflare AI", zap.Error(err))
		return respBody, err
	}

	return newRespBody, nil
}
