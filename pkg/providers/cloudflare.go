package providers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/neutrome-labs/caddy-ai-router/pkg/common"
	"github.com/neutrome-labs/caddy-ai-router/pkg/transforms"
	"go.uber.org/zap"
)

// CloudflareProvider implements the Provider interface for Cloudflare.
type CloudflareProvider struct{}

// Name returns the name of the provider.
func (p *CloudflareProvider) Name() string {
	return "cloudflare"
}

// ModifyCompletionRequest sets the URL path for the completion request.
func (p *CloudflareProvider) ModifyCompletionRequest(r *http.Request, modelName string, logger *zap.Logger) error {
	r.URL.Path = strings.TrimRight(r.URL.Path, "/") + "/run/" + modelName

	common.HookHttpRequestBody(r, func(r *http.Request, body []byte) ([]byte, error) {
		transformedBody, err := transforms.TransformRequestToCloudflareAI(r, body, modelName, logger)
		if err != nil {
			logger.Error("Failed to transform request body for Cloudflare AI", zap.Error(err))
			return nil, err
		}
		return transformedBody, nil
	})

	return nil
}

// ModifyCompletionResponse is a no-op for Cloudflare.
func (p *CloudflareProvider) ModifyCompletionResponse(w http.ResponseWriter, r *http.Request, resp *http.Response, logger *zap.Logger) error {
	// convert from responses fromat to default
	// eg {"response":"","usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}} to {"choices":[{"text":"","index":0,"logprobs":null,"finish_reason":""}]}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("Failed to read response body for Cloudflare AI", zap.Error(err))
		return err
	}

	respBodyStr := string(respBody)

	// Check if this is a streaming response with multiple data chunks
	if strings.Contains(respBodyStr, "data: ") {
		// Split by "data: " and process each chunk
		chunks := strings.Split(respBodyStr, "data: ")
		var transformedChunks []string

		for _, chunk := range chunks {
			chunk = strings.TrimSpace(chunk)
			if chunk == "" {
				continue
			}

			var respBodyJson map[string]any
			if err := json.Unmarshal([]byte(chunk), &respBodyJson); err != nil {
				logger.Error("Failed to decode response chunk for Cloudflare AI", zap.Error(err))
				continue
			}

			// Map Cloudflare's response format to the default format
			defaultResp := map[string]any{
				"choices": []map[string]any{
					{
						"message": map[string]any{
							"role":    "assistant",
							"content": respBodyJson["response"],
						},
						"index":         0,
						"logprobs":      nil,
						"finish_reason": "",
					},
				},
			}

			newChunkBody, err := json.Marshal(defaultResp)
			if err != nil {
				logger.Error("Failed to marshal response chunk for Cloudflare AI", zap.Error(err))
				continue
			}

			transformedChunks = append(transformedChunks, "data: "+string(newChunkBody))
		}

		newRespBodyString := strings.Join(transformedChunks, "\n\n")
		resp.Body = io.NopCloser(strings.NewReader(newRespBodyString))
	} else {
		// Handle single JSON response
		var respBodyJson map[string]any
		if err := json.Unmarshal(respBody, &respBodyJson); err != nil {
			logger.Error("Failed to decode response body for Cloudflare AI", zap.Error(err))
			return err
		}

		// Map Cloudflare's response format to the default format
		defaultResp := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"role":    "assistant",
						"content": respBodyJson["result"].(map[string]interface{})["response"],
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
			return err
		}

		resp.Body = io.NopCloser(strings.NewReader(string(newRespBody)))
	}

	return nil
}

// FetchModels fetches the models from the Cloudflare API.
func (p *CloudflareProvider) FetchModels(baseURL string, apiKey string, httpClient *http.Client, logger *zap.Logger) ([]map[string]any, error) {
	base := strings.TrimRight(baseURL, "/") + "/models/search"

	type cursors struct {
		After string `json:"after"`
	}
	type resultInfo struct {
		Page       int      `json:"page"`
		PerPage    int      `json:"per_page"`
		TotalPages int      `json:"total_pages"`
		Count      int      `json:"count"`
		TotalCount int      `json:"total_count"`
		Cursors    *cursors `json:"cursors"`
	}
	type response struct {
		Result     []map[string]any `json:"result"`
		ResultInfo *resultInfo      `json:"result_info"`
	}

	var all []map[string]any

	// Prefer cursor-based pagination if provided; fallback to page/per_page
	page := 1
	perPage := 100
	cursor := ""

	for i := 0; i < 1000; i++ { // hard upper bound to prevent infinite loops
		u, err := url.Parse(base)
		if err != nil {
			return nil, fmt.Errorf("invalid models URL %s: %w", base, err)
		}
		q := u.Query()
		if cursor != "" {
			q.Set("cursor", cursor)
		} else {
			q.Set("page", fmt.Sprintf("%d", page))
			q.Set("per_page", fmt.Sprintf("%d", perPage))
		}
		u.RawQuery = q.Encode()

		req, err := http.NewRequest(http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request for %s: %w", u.String(), err)
		}
		req.Header.Set("User-Agent", "Caddy-AI-Router")
		if apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}

		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("request to %s failed: %w", u.String(), err)
		}
		if resp.Body != nil {
			defer resp.Body.Close()
		}

		if resp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(resp.Body)
			return nil, fmt.Errorf("request to %s returned status %d: %s", u.String(), resp.StatusCode, string(bodyBytes))
		}

		var pr response
		if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
			return nil, fmt.Errorf("failed to decode response from %s: %w", u.String(), err)
		}

		for _, model := range pr.Result {
			if name, ok := model["name"].(string); ok {
				all = append(all, map[string]any{
					"id":   name,
					"name": name,
				})
			}
		}

		// Decide next page based on result_info
		if pr.ResultInfo != nil {
			if pr.ResultInfo.Cursors != nil && pr.ResultInfo.Cursors.After != "" {
				cursor = pr.ResultInfo.Cursors.After
				// continue with cursor mode; do not increment page
				continue
			}
			// Fallback to page/total_pages
			if pr.ResultInfo.TotalPages > 0 {
				if page >= pr.ResultInfo.TotalPages {
					break
				}
				page++
				continue
			}
			// If no explicit totals, stop when less than requested perPage
			if pr.ResultInfo.Count < perPage || len(pr.Result) < perPage {
				break
			}
			page++
			continue
		}

		// If no result_info at all, stop after first page
		break
	}

	return all, nil
}
