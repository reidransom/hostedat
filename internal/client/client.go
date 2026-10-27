package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Client struct {
	BaseURL    string
	Token      string
	Version    string
	HTTPClient *http.Client

	// AllowInsecureLoopback explicitly opts into HTTP for local development only.
	// It never permits remote HTTP, redirects, or disabled TLS verification.
	AllowInsecureLoopback bool
	safeErrors            bool
}

type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("API error %d: %s", e.StatusCode, e.Message)
}

func New(baseURL, token string) *Client {
	return &Client{
		BaseURL: baseURL,
		Token:   token,
		HTTPClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func (c *Client) do(method, path string, body io.Reader, contentType string) (*http.Response, error) {
	url := c.BaseURL + path
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}

	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if c.Version != "" {
		req.Header.Set("X-Hostedat-Version", c.Version)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	// Never replay a write body, or forward authorization, via a redirect.
	// Copy the caller's client so its transport/timeout remain intact.
	httpClient := *c.HTTPClient
	httpClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		_ = resp.Body.Close()
		return nil, &APIError{StatusCode: resp.StatusCode, Message: "redirect refused; use the server's final URL"}
	}

	if resp.StatusCode >= 400 {
		defer func() { _ = resp.Body.Close() }()
		if c.safeErrors {
			return nil, &APIError{StatusCode: resp.StatusCode, Message: http.StatusText(resp.StatusCode)}
		}
		var errResp struct {
			Error string `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
			return nil, &APIError{StatusCode: resp.StatusCode, Message: http.StatusText(resp.StatusCode)}
		}
		return nil, &APIError{StatusCode: resp.StatusCode, Message: errResp.Error}
	}

	return resp, nil
}

func (c *Client) doJSON(method, path string, reqBody, respBody interface{}) error {
	var body io.Reader
	if reqBody != nil {
		data, err := json.Marshal(reqBody)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}

	resp, err := c.do(method, path, body, "application/json")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if respBody != nil {
		return json.NewDecoder(resp.Body).Decode(respBody)
	}
	return nil
}

func (c *Client) get(path string, result interface{}) error {
	return c.doJSON(http.MethodGet, path, nil, result)
}

func (c *Client) post(path string, reqBody, result interface{}) error {
	return c.doJSON(http.MethodPost, path, reqBody, result)
}

func (c *Client) patch(path string, reqBody, result interface{}) error {
	return c.doJSON(http.MethodPatch, path, reqBody, result)
}

func (c *Client) delete(path string, result interface{}) error {
	return c.doJSON(http.MethodDelete, path, nil, result)
}
