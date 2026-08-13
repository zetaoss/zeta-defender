package prometheus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
)

const maxResponseBytes = 4 << 20

type Provider struct {
	endpoint *url.URL
	expr     string
	client   *http.Client
}

func New(endpoint, expr string, client *http.Client) (*Provider, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse prometheus endpoint: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("prometheus endpoint must use http or https")
	}
	if u.Host == "" {
		return nil, errors.New("prometheus endpoint must include a host")
	}
	if strings.TrimSpace(expr) == "" {
		return nil, errors.New("prometheus expression is required")
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &Provider{endpoint: u, expr: expr, client: client}, nil
}

type response struct {
	Status    string `json:"status"`
	ErrorType string `json:"errorType"`
	Error     string `json:"error"`
	Data      struct {
		ResultType string          `json:"resultType"`
		Result     json.RawMessage `json:"result"`
	} `json:"data"`
}

func (p *Provider) Evaluate(ctx context.Context) (bool, error) {
	u := *p.endpoint
	u.Path = path.Join(strings.TrimSuffix(u.Path, "/"), "/api/v1/query")
	q := u.Query()
	q.Set("query", p.expr)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return false, fmt.Errorf("create prometheus request: %w", err)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("query prometheus: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return false, fmt.Errorf("read prometheus response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return false, errors.New("prometheus response exceeds 4 MiB")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Errorf("prometheus returned HTTP %s", resp.Status)
	}
	var result response
	if err := json.Unmarshal(body, &result); err != nil {
		return false, fmt.Errorf("decode prometheus response: %w", err)
	}
	if result.Status != "success" {
		return false, fmt.Errorf("prometheus query failed (%s): %s", result.ErrorType, result.Error)
	}
	return booleanResult(result.Data.ResultType, result.Data.Result)
}

func booleanResult(resultType string, raw json.RawMessage) (bool, error) {
	switch resultType {
	case "scalar":
		var value []json.RawMessage
		if err := json.Unmarshal(raw, &value); err != nil || len(value) != 2 {
			return false, errors.New("invalid Prometheus scalar result")
		}
		return parseValue(value[1])
	case "vector":
		var values []struct {
			Value []json.RawMessage `json:"value"`
		}
		if err := json.Unmarshal(raw, &values); err != nil {
			return false, fmt.Errorf("decode Prometheus vector: %w", err)
		}
		for _, sample := range values {
			if len(sample.Value) != 2 {
				return false, errors.New("invalid Prometheus vector sample")
			}
			v, err := parseValue(sample.Value[1])
			if err != nil {
				return false, err
			}
			if v {
				return true, nil
			}
		}
		return false, nil
	default:
		return false, fmt.Errorf("Prometheus result type %q is not boolean-compatible", resultType)
	}
}

func parseValue(raw json.RawMessage) (bool, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return false, errors.New("Prometheus sample value is not a string")
	}
	v, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return false, fmt.Errorf("Prometheus sample value %q is not finite numeric data", text)
	}
	return v != 0, nil
}
