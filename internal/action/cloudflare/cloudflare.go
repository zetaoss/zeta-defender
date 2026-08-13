package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

const (
	defaultAPIBase  = "https://api.cloudflare.com/client/v4"
	maxResponseSize = 1 << 20
)

type Action struct {
	token  string
	zoneID string
	base   string
	client *http.Client

	mu            sync.Mutex
	previousLevel string
	owned         bool
}

func New(apiToken, zoneID string, client *http.Client) (*Action, error) {
	return newWithBase(apiToken, zoneID, defaultAPIBase, client)
}

func newWithBase(apiToken, zoneID, base string, client *http.Client) (*Action, error) {
	if strings.TrimSpace(apiToken) == "" || strings.TrimSpace(zoneID) == "" {
		return nil, errors.New("Cloudflare API token and zone ID are required")
	}
	if _, err := url.ParseRequestURI(base); err != nil {
		return nil, fmt.Errorf("invalid Cloudflare API base URL: %w", err)
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &Action{token: apiToken, zoneID: zoneID, base: strings.TrimRight(base, "/"), client: client}, nil
}

func (a *Action) Activate(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	current, err := a.getLevel(ctx)
	if err != nil {
		return err
	}
	if current == "under_attack" {
		return nil
	}
	if err := a.setLevel(ctx, "under_attack"); err != nil {
		return err
	}
	a.previousLevel = current
	a.owned = true
	return nil
}

func (a *Action) Deactivate(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Never modify protection that this process did not successfully enable.
	if !a.owned {
		return nil
	}

	current, err := a.getLevel(ctx)
	if err != nil {
		return err
	}
	if current != "under_attack" {
		a.previousLevel = ""
		a.owned = false
		return nil
	}

	target := a.previousLevel
	if err := a.setLevel(ctx, target); err != nil {
		return err
	}
	a.previousLevel = ""
	a.owned = false
	return nil
}

type apiResponse struct {
	Success bool `json:"success"`
	Result  struct {
		Value string `json:"value"`
	} `json:"result"`
	Errors []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
}

func (a *Action) getLevel(ctx context.Context) (string, error) {
	resp, err := a.request(ctx, http.MethodGet, nil)
	if err != nil {
		return "", err
	}
	if resp.Result.Value == "" {
		return "", errors.New("Cloudflare returned an empty security level")
	}
	return resp.Result.Value, nil
}

func (a *Action) setLevel(ctx context.Context, level string) error {
	body, err := json.Marshal(map[string]string{"value": level})
	if err != nil {
		return err
	}
	_, err = a.request(ctx, http.MethodPatch, body)
	return err
}

func (a *Action) request(ctx context.Context, method string, body []byte) (apiResponse, error) {
	endpoint := a.base + "/zones/" + url.PathEscape(a.zoneID) + "/settings/security_level"
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return apiResponse{}, fmt.Errorf("create Cloudflare request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return apiResponse{}, fmt.Errorf("call Cloudflare API: %w", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		return apiResponse{}, fmt.Errorf("read Cloudflare response: %w", err)
	}
	if len(b) > maxResponseSize {
		return apiResponse{}, errors.New("Cloudflare response exceeds 1 MiB")
	}
	var result apiResponse
	if err := json.Unmarshal(b, &result); err != nil {
		return apiResponse{}, fmt.Errorf("decode Cloudflare response (HTTP %s): %w", resp.Status, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !result.Success {
		if len(result.Errors) > 0 {
			return apiResponse{}, fmt.Errorf("Cloudflare API failed (HTTP %s, code %d): %s", resp.Status, result.Errors[0].Code, result.Errors[0].Message)
		}
		return apiResponse{}, fmt.Errorf("Cloudflare API failed (HTTP %s)", resp.Status)
	}
	return result, nil
}
