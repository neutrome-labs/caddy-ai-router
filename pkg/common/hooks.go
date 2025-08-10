package common

import (
	"bytes"
	"io"
	"net/http"
	"strings"
)

func HookHttpRequestBody(r *http.Request, transform func(r *http.Request, body []byte) ([]byte, error)) error {
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	r.Body.Close()

	transformedBody, err := transform(r, bodyBytes)
	if err != nil {
		return err
	}

	r.Body = io.NopCloser(bytes.NewBuffer(transformedBody))
	r.ContentLength = int64(len(transformedBody))

	return nil
}

func HookHttpResponseBody(resp *http.Response, transform func(resp *http.Response, body []byte) ([]byte, error)) error {
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	resp.Body.Close()

	transformedBody, err := transform(resp, bodyBytes)
	if err != nil {
		return err
	}

	resp.Body = io.NopCloser(bytes.NewBuffer(transformedBody))
	resp.ContentLength = int64(len(transformedBody))

	return nil
}

func HookHttpResponseJsonChunks(transform func(body []byte) ([]byte, error)) func(resp *http.Response, body []byte) ([]byte, error) {
	return func(resp *http.Response, body []byte) ([]byte, error) {
		if resp.Header.Get("Content-Type") == "application/json" {
			return transform(body)
		} else if resp.Header.Get("Content-Type") == "text/event-stream" {
			chunks := strings.Split(string(body), "data: ")
			var transformedChunks []string

			for _, chunk := range chunks {
				chunk = strings.TrimSpace(chunk)
				if chunk == "" || chunk == "[DONE]" {
					continue
				}

				newChunkBody, err := transform([]byte(chunk))
				if err != nil {
					return body, err
				}

				transformedChunks = append(transformedChunks, "data: "+string(newChunkBody))
			}

			return []byte(strings.Join(transformedChunks, "\n\n")), nil
		}

		return body, nil
	}
}
