package transforms

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/neutrome-labs/caddy-ai-router/pkg/common"
	"go.uber.org/zap"
)

// --- Google AI Style Structures ---

// GoogleAIPart defines a part of a Google AI content message.
type GoogleAIPart struct {
	Text string `json:"text,omitempty"`
	// InlineData, FileData etc. could be added here
}

// GoogleAIContent defines a content block in a Google AI request/response.
type GoogleAIContent struct {
	Role  string         `json:"role"` // "user" or "model"
	Parts []GoogleAIPart `json:"parts"`
}

// GoogleAIGenerateContentRequest defines the request structure for Google AI's generateContent.
type GoogleAIGenerateContentRequest struct {
	Contents []GoogleAIContent `json:"contents"`
	// GenerationConfig, SafetySettings, etc. can be added here.
	// Model name is typically part of the URL for Google AI.
}

// GoogleAICandidate defines a candidate response from Google AI.
type GoogleAICandidate struct {
	Content      GoogleAIContent `json:"content"`
	FinishReason string          `json:"finishReason"`
	Index        int32           `json:"index"`
	// SafetyRatings, CitationMetadata, etc.
}

// GoogleAIPromptFeedback defines feedback on the prompt.
type GoogleAIPromptFeedback struct {
	BlockReason string `json:"blockReason,omitempty"`
	// SafetyRatings
}

// GoogleAIGenerateContentResponse defines the response structure from Google AI's generateContent.
type GoogleAIGenerateContentResponse struct {
	Candidates     []GoogleAICandidate     `json:"candidates"`
	PromptFeedback *GoogleAIPromptFeedback `json:"promptFeedback,omitempty"`
	// UsageMetadata (for token counts) would be part of this if available directly.
	// Often token counts for Google AI are estimated or provided differently.
}

func TransformRequestToGoogleAI(r *http.Request, originalBody []byte, modelName string, logger *zap.Logger) ([]byte, error) {
	// Move API key from header to query param
	apiKey := r.Header.Get("Authorization")
	if strings.HasPrefix(apiKey, "Bearer ") {
		apiKey = strings.TrimPrefix(apiKey, "Bearer ")
	}
	if apiKey != "" {
		q := r.URL.Query()
		q.Set("key", apiKey)
		r.URL.RawQuery = q.Encode()
		r.Header.Del("Authorization") // Remove original auth header
		logger.Debug("Moved API key from Authorization header to 'key' query parameter for Google AI")
	}

	var unifiedReq UnifiedChatRequest
	if err := json.Unmarshal(originalBody, &unifiedReq); err != nil {
		logger.Error("Failed to unmarshal original request for Google AI transformation", zap.Error(err), zap.ByteString("body", originalBody))
		return nil, fmt.Errorf("unmarshal original request for Google AI: %w", err)
	}

	googleReq := GoogleAIGenerateContentRequest{
		Contents: make([]GoogleAIContent, 0, len(unifiedReq.Messages)),
	}

	for _, msg := range unifiedReq.Messages {
		role := "user" // Default for Google
		if msg.Role == "assistant" {
			role = "model"
		} else if msg.Role == "system" {
			// Google's Gemini API handles system instructions differently (often via a specific field or by prepending to the first user message).
			// For simplicity, we'll convert a system message to a user message if it's the first one,
			// or a model message (as context) if it's not. This is a simplification.
			// A more robust solution would involve checking Google's specific model capabilities.
			logger.Info("Transforming system message for Google AI", zap.String("content", msg.Content))
			if len(googleReq.Contents) == 0 {
				role = "user" // Treat as initial user prompt part
			} else {
				role = "model" // Treat as part of the ongoing conversation history
			}
		}
		googleReq.Contents = append(googleReq.Contents, GoogleAIContent{
			Role:  role,
			Parts: []GoogleAIPart{{Text: msg.Content}},
		})
	}

	transformedBody, err := json.Marshal(googleReq)
	if err != nil {
		logger.Error("Failed to marshal request for Google AI transformation", zap.Error(err))
		return nil, fmt.Errorf("marshal Google AI request: %w", err)
	}
	logger.Debug("Transformed request to Google AI style", zap.ByteString("transformed_body", transformedBody))
	return transformedBody, nil
}

func TransformResponseFromGoogleAI(respBody []byte, logger *zap.Logger) ([]byte, error) {
	var googleResp GoogleAIGenerateContentResponse
	if err := json.Unmarshal(respBody, &googleResp); err != nil {
		logger.Error("Failed to unmarshal google response", zap.Error(err), zap.ByteString("body", respBody))
		// Return original body if unmarshalling fails
		return respBody, nil
	}

	unifiedResp := UnifiedChatResponse{
		// ID and Created would need to be generated or mapped if available
		// For simplicity, let's generate a new one.
		ID:      "gen-" + fmt.Sprintf("%d", common.CaddyClock.Now().Unix()),
		Object:  "chat.completion",
		Created: common.CaddyClock.Now().Unix(),
		Choices: make([]UnifiedChoice, 0, len(googleResp.Candidates)),
	}

	if len(googleResp.Candidates) > 0 {
		// Assuming the first candidate is the primary one
		candidate := googleResp.Candidates[0]
		unifiedResp.Model = candidate.Content.Role // Or a static model name passed in
		unifiedResp.Choices = append(unifiedResp.Choices, UnifiedChoice{
			Index: 0,
			Message: UnifiedChatMessage{
				Role:    "assistant",
				Content: candidate.Content.Parts[0].Text,
			},
			FinishReason: candidate.FinishReason,
		})
	}

	// Note: Google AI API's token count (UsageMetadata) might not be directly in the response
	// and could require a separate call or estimation. For now, we'll omit it.
	// unifiedResp.Usage = &UnifiedUsage{ ... }

	transformedBytes, err := json.Marshal(unifiedResp)
	if err != nil {
		logger.Error("Failed to marshal unified response from google", zap.Error(err))
		return nil, fmt.Errorf("marshaling unified response from google: %w", err)
	}

	return transformedBytes, nil
}
