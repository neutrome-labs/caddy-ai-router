package transforms

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/neutrome-labs/caddy-ai-router/pkg/common"
	"go.uber.org/zap"
)

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

func TransformRequestToAnthropic(r *http.Request, originalBody []byte, modelName string, logger *zap.Logger) ([]byte, error) {
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

func TransformResponseFromAnthropic(respBody []byte, logger *zap.Logger) ([]byte, error) {
	var anthropicResp AnthropicMessagesResponse
	if err := json.Unmarshal(respBody, &anthropicResp); err != nil {
		logger.Error("Failed to unmarshal anthropic response", zap.Error(err), zap.ByteString("body", respBody))
		return respBody, nil
	}

	unifiedResp := UnifiedChatResponse{
		ID:      anthropicResp.ID,
		Object:  "chat.completion",
		Created: common.CaddyClock.Now().Unix(), // Generate timestamp
		Model:   anthropicResp.Model,
		Choices: make([]UnifiedChoice, 0, len(anthropicResp.Content)),
		Usage: &UnifiedUsage{
			PromptTokens:     anthropicResp.Usage.InputTokens,
			CompletionTokens: anthropicResp.Usage.OutputTokens,
			TotalTokens:      anthropicResp.Usage.InputTokens + anthropicResp.Usage.OutputTokens,
		},
	}

	if len(anthropicResp.Content) > 0 {
		unifiedResp.Choices = append(unifiedResp.Choices, UnifiedChoice{
			Index: 0,
			Message: UnifiedChatMessage{
				Role:    "assistant",
				Content: anthropicResp.Content[0].Text,
			},
			FinishReason: anthropicResp.StopReason,
		})
	}

	transformedBytes, err := json.Marshal(unifiedResp)
	if err != nil {
		logger.Error("Failed to marshal unified response from anthropic", zap.Error(err))
		return nil, fmt.Errorf("marshaling unified response from anthropic: %w", err)
	}

	return transformedBytes, nil
}
