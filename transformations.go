package ai_router

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"go.uber.org/zap"
)

// --- Unified (OpenAI-like) Structures ---

// UnifiedChatMessage defines the structure for a single message in a chat.
type UnifiedChatMessage struct {
	Role    string `json:"role"` // e.g., "user", "assistant", "system"
	Content string `json:"content"`
}

// UnifiedChatRequest defines the structure for a chat completion request.
type UnifiedChatRequest struct {
	Model       string               `json:"model"`
	Messages    []UnifiedChatMessage `json:"messages"`
	Stream      bool                 `json:"stream,omitempty"`
	MaxTokens   *int                 `json:"max_tokens,omitempty"` // Pointer to distinguish between not set and 0
	Temperature *float64             `json:"temperature,omitempty"`
	// Add other common fields as needed
}

// UnifiedChoice defines a single choice in a chat completion response.
type UnifiedChoice struct {
	Index        int                `json:"index"`
	Message      UnifiedChatMessage `json:"message"`
	FinishReason string             `json:"finish_reason,omitempty"`
}

// UnifiedUsage defines the token usage for a request.
type UnifiedUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// UnifiedChatResponse defines the structure for a chat completion response.
type UnifiedChatResponse struct {
	ID      string          `json:"id"`
	Object  string          `json:"object"` // e.g., "chat.completion"
	Created int64           `json:"created"`
	Model   string          `json:"model"`
	Choices []UnifiedChoice `json:"choices"`
	Usage   *UnifiedUsage   `json:"usage,omitempty"`
}

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

// --- Anthropic Style Structures ---

// AnthropicMessage defines a message in Anthropic's Messages API.
type AnthropicMessage struct {
	Role    string `json:"role"`    // "user" or "assistant"
	Content string `json:"content"` // Can also be an array of content blocks
}

// AnthropicMessagesRequest defines the request for Anthropic's Messages API.
type AnthropicMessagesRequest struct {
	Model       string             `json:"model"`
	Messages    []AnthropicMessage `json:"messages"`
	System      string             `json:"system,omitempty"`
	MaxTokens   int                `json:"max_tokens"`
	Stream      bool               `json:"stream,omitempty"`
	Temperature *float64           `json:"temperature,omitempty"`
	// TopP, TopK, StopSequences, etc.
}

// AnthropicMessagesResponse defines the response from Anthropic's Messages API.
type AnthropicMessagesResponse struct {
	ID           string                  `json:"id"`
	Type         string                  `json:"type"` // e.g., "message"
	Role         string                  `json:"role"` // "assistant"
	Content      []AnthropicContentBlock `json:"content"`
	Model        string                  `json:"model"`
	StopReason   string                  `json:"stop_reason"` // "end_turn", "max_tokens", "stop_sequence"
	StopSequence *string                 `json:"stop_sequence,omitempty"`
	Usage        AnthropicUsage          `json:"usage"`
}

// AnthropicContentBlock defines a block of content in Anthropic's response.
type AnthropicContentBlock struct {
	Type string `json:"type"` // e.g., "text"
	Text string `json:"text,omitempty"`
}

// AnthropicUsage defines token usage for Anthropic.
type AnthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// --- Request Transformation Functions ---

func transformModelRequest(style string, r *http.Request, logger *zap.Logger) error {
	switch style {
	case "genai":
		// For /models, we only need to adjust headers/query params, not the body.
		apiKey := r.Header.Get("Authorization")
		if strings.HasPrefix(apiKey, "Bearer ") {
			apiKey = strings.TrimPrefix(apiKey, "Bearer ")
		}
		if apiKey != "" {
			q := r.URL.Query()
			q.Set("key", apiKey)
			r.URL.RawQuery = q.Encode()
			r.Header.Del("Authorization")
			logger.Debug("Moved API key to query param for Google AI /models request")
		}
	// Add other cases for different styles if they need /models request transformation.
	default:
		logger.Debug("No special /models transformation needed for style", zap.String("style", style))
	}
	return nil
}

func transformRequest(style string, r *http.Request, modelName string, logger *zap.Logger) error {
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		logger.Error("Failed to read request body for transformation", zap.Error(err))
		return fmt.Errorf("reading request body for transformation: %w", err)
	}
	r.Body.Close() // Close original body

	var finalBodyBytes []byte
	var transformErr error

	switch style {
	case "genai":
		finalBodyBytes, transformErr = transformRequestToGoogleAI(r, bodyBytes, modelName, logger)
	case "anthropic":
		finalBodyBytes, transformErr = transformRequestToAnthropic(r, bodyBytes, modelName, logger)
	default:
		logger.Warn("Unknown transformation style, proceeding with original body", zap.String("style", style))
		finalBodyBytes = bodyBytes // Passthrough
	}

	if transformErr != nil {
		return transformErr // The error is already logged by the specific function
	}

	// Set the final transformed body
	r.Body = io.NopCloser(bytes.NewBuffer(finalBodyBytes))
	r.ContentLength = int64(len(finalBodyBytes))
	r.Header.Set("Content-Type", "application/json") // Transformations assume JSON output

	return nil
}

func transformRequestToGoogleAI(r *http.Request, originalBody []byte, modelName string, logger *zap.Logger) ([]byte, error) {
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
	// Note: Model name for Google is often in the URL. This transformation focuses on the body.
	// The `UpstreamPath` in Caddyfile or proxy director should handle model in URL.

	transformedBody, err := json.Marshal(googleReq)
	if err != nil {
		logger.Error("Failed to marshal request for Google AI transformation", zap.Error(err))
		return nil, fmt.Errorf("marshal Google AI request: %w", err)
	}
	logger.Debug("Transformed request to Google AI style", zap.ByteString("transformed_body", transformedBody))
	return transformedBody, nil
}

func transformRequestToAnthropic(r *http.Request, originalBody []byte, modelName string, logger *zap.Logger) ([]byte, error) {
	var unifiedReq UnifiedChatRequest
	if err := json.Unmarshal(originalBody, &unifiedReq); err != nil {
		logger.Error("Failed to unmarshal original request for Anthropic transformation", zap.Error(err), zap.ByteString("body", originalBody))
		return nil, fmt.Errorf("unmarshal original request for Anthropic: %w", err)
	}

	anthropicReq := AnthropicMessagesRequest{
		Model:     modelName, // Anthropic expects model in the body
		Messages:  make([]AnthropicMessage, 0, len(unifiedReq.Messages)),
		MaxTokens: 1024, // Default, should come from unifiedReq if available
		Stream:    unifiedReq.Stream,
	}
	if unifiedReq.MaxTokens != nil {
		anthropicReq.MaxTokens = *unifiedReq.MaxTokens
	}
	if unifiedReq.Temperature != nil {
		anthropicReq.Temperature = unifiedReq.Temperature
	}

	for _, msg := range unifiedReq.Messages {
		if msg.Role == "system" {
			if anthropicReq.System != "" {
				anthropicReq.System += "\n" + msg.Content
			} else {
				anthropicReq.System = msg.Content
			}
			continue
		}
		role := "user"
		if msg.Role == "assistant" {
			role = "assistant"
		} else if msg.Role != "user" {
			logger.Warn("Unsupported role for Anthropic transformation, defaulting to 'user'", zap.String("original_role", msg.Role))
		}
		anthropicReq.Messages = append(anthropicReq.Messages, AnthropicMessage{
			Role:    role,
			Content: msg.Content,
		})
	}

	transformedBody, err := json.Marshal(anthropicReq)
	if err != nil {
		logger.Error("Failed to marshal request for Anthropic transformation", zap.Error(err))
		return nil, fmt.Errorf("marshal Anthropic request: %w", err)
	}
	logger.Debug("Transformed request to Anthropic style", zap.ByteString("transformed_body", transformedBody))
	return transformedBody, nil
}

// --- Response Transformation Functions (Placeholders) ---

// transformResponse converts a provider-specific response body to the unified format.
// For now, these are placeholders and will pass through the original body.
func transformResponse(respBody io.ReadCloser, style string, logger *zap.Logger) (io.ReadCloser, error) {
	bodyBytes, err := io.ReadAll(respBody)
	if err != nil {
		logger.Error("Failed to read response body for transformation", zap.Error(err), zap.String("style", style))
		// Return an error, let ModifyResponse handler decide.
		return nil, fmt.Errorf("reading original response body for %s: %w", style, err)
	}
	respBody.Close() // Close the original body as we've read it.

	var transformedBytes []byte = bodyBytes // Default to passthrough

	switch style {
	case "genai":
		logger.Debug("Attempting to transform response from Google AI style (currently passthrough)")
		// Placeholder: Convert GoogleAIGenerateContentResponse to UnifiedChatResponse
		// var googleResp GoogleAIGenerateContentResponse
		// if err := json.Unmarshal(bodyBytes, &googleResp); err != nil { ... }
		// var unifiedResp UnifiedChatResponse = ... map googleResp to unifiedResp ...
		// transformedBytes, err = json.Marshal(unifiedResp)
		// if err != nil { ... }
		transformedBytes = bodyBytes // Actual transformation logic needed here
	case "anthropic":
		logger.Debug("Attempting to transform response from Anthropic style (currently passthrough)")
		// Placeholder: Convert AnthropicMessagesResponse to UnifiedChatResponse
		// var anthropicResp AnthropicMessagesResponse
		// if err := json.Unmarshal(bodyBytes, &anthropicResp); err != nil { ... }
		// var unifiedResp UnifiedChatResponse = ... map anthropicResp to unifiedResp ...
		// transformedBytes, err = json.Marshal(unifiedResp)
		// if err != nil { ... }
		transformedBytes = bodyBytes // Actual transformation logic needed here
	default:
		logger.Warn("Unknown transformation style for response, passing through", zap.String("style", style))
		// transformedBytes remains bodyBytes
	}

	// If actual transformation happened and failed, 'err' would be set by marshal.
	// For now, errors are mainly from initial read or if future marshal fails.
	// The current placeholder logic doesn't re-marshal, so 'err' from above is the main concern.

	return io.NopCloser(bytes.NewBuffer(transformedBytes)), nil
}
