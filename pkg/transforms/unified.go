package transforms

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
