package tmdb

import (
	"errors"
	"fmt"
	"io"
	"net/http"
)

type rawProviderResponse struct {
	body   []byte
	status int
}

func (r rawProviderResponse) GetBody() []byte { return r.body }

func (r rawProviderResponse) StatusCode() int { return r.status }

func readProviderResponse(response *http.Response, callErr error) (rawProviderResponse, error) {
	status := 0
	if response != nil {
		status = response.StatusCode
	}
	result := rawProviderResponse{status: status}
	if callErr != nil {
		closeProviderResponse(response)
		return result, callErr
	}
	if response == nil {
		return result, errors.New("TMDB client returned no response")
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return result, responseReadError(status, err)
	}
	if int64(len(body)) > maxResponseBytes {
		err := fmt.Errorf("TMDB response exceeds %d bytes", maxResponseBytes)
		return result, newResponseReceivedError(status, err)
	}
	result.body = body
	return result, nil
}

func responseReadError(status int, err error) error {
	if responseErr, ok := errors.AsType[*responseReceivedError](err); ok && responseErr != nil {
		return err
	}
	return newResponseReceivedError(status, err)
}

func closeProviderResponse(response *http.Response) {
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
}
