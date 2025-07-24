package common

import (
	"bytes"
	"io"
	"net/http"
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
