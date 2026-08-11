package pipeline

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
	"time"
)

const maxMermaidSourceBytes = 20_000

type MermaidValidator interface {
	Validate(ctx context.Context, source string) error
}

type HTTPMermaidValidator struct {
	endpoint string
	secret   string
	client   *http.Client
}

func NewHTTPMermaidValidator(dashboardBaseURL, secret string, client *http.Client) *HTTPMermaidValidator {
	endpoint := ""
	if base, err := url.Parse(strings.TrimRight(dashboardBaseURL, "/")); err == nil && base.Scheme != "" && base.Host != "" {
		base.Path = strings.TrimRight(base.Path, "/") + "/api/internal/mermaid/validate"
		base.RawQuery = ""
		base.Fragment = ""
		endpoint = base.String()
	}
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	return &HTTPMermaidValidator{endpoint: endpoint, secret: secret, client: client}
}

func (v *HTTPMermaidValidator) Validate(ctx context.Context, source string) error {
	if v == nil || v.endpoint == "" || v.secret == "" {
		return errors.New("mermaid validator endpoint or secret is not configured")
	}
	if len(source) > maxMermaidSourceBytes {
		return fmt.Errorf("mermaid source exceeds %d bytes", maxMermaidSourceBytes)
	}
	body, err := json.Marshal(struct {
		Source string `json:"source"`
	}{Source: source})
	if err != nil {
		return fmt.Errorf("marshal mermaid validation request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build mermaid validation request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Argus-Mermaid-Secret", v.secret)
	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("call mermaid validator: %w", err)
	}
	defer resp.Body.Close()
	var result struct {
		Valid   bool   `json:"valid"`
		Version string `json:"version"`
		Error   string `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<10)).Decode(&result); err != nil {
		return fmt.Errorf("decode mermaid validator response: %w", err)
	}
	if resp.StatusCode != http.StatusOK || !result.Valid || result.Version == "" {
		return fmt.Errorf("mermaid parser rejected source: status=%d version=%q reason=%q", resp.StatusCode, result.Version, result.Error)
	}
	return nil
}
