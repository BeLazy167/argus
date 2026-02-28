package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const baseURL = "https://api.supermemory.ai"

// Client wraps the Supermemory REST API for memory storage and retrieval.
type Client struct {
	apiKey string
	client *http.Client
}

func NewClient(apiKey string) *Client {
	return &Client{
		apiKey: apiKey,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// doJSON posts reqBody as JSON to the given path and decodes the response into result.
func (c *Client) doJSON(ctx context.Context, path string, reqBody, result any) error {
	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshaling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	c.setHeaders(httpReq)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("supermemory %s error (status %d): %s", path, resp.StatusCode, string(respBody))
	}

	if err := json.Unmarshal(respBody, result); err != nil {
		return fmt.Errorf("unmarshaling response: %w", err)
	}
	return nil
}

// AddMemory stores a new memory in Supermemory.
func (c *Client) AddMemory(ctx context.Context, req AddRequest) (*AddResponse, error) {
	var result AddResponse
	if err := c.doJSON(ctx, "/v3/documents", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Search performs semantic search across memories.
func (c *Client) Search(ctx context.Context, req SearchRequest) (*SearchResponse, error) {
	var result SearchResponse
	if err := c.doJSON(ctx, "/v4/search", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
}

// tagSanitizer replaces chars invalid in Supermemory container tags.
var tagSanitizer = strings.NewReplacer(":", "-", "/", "-")

// OwnerTag returns a container tag scoped to an owner (user or org).
// Uses ~ as segment separator to avoid collisions (e.g. owner "a-b" vs "a" repo "b").
func OwnerTag(owner, kind string) string {
	return tagSanitizer.Replace(owner) + "~" + kind
}

// RepoTag returns a container tag scoped to a specific repo under an owner.
func RepoTag(owner, repo, kind string) string {
	return tagSanitizer.Replace(owner) + "~" + tagSanitizer.Replace(repo) + "~" + kind
}

// ValidateTagScope checks that a container tag belongs to the given owner.
func ValidateTagScope(tag, owner string) bool {
	return strings.HasPrefix(tag, tagSanitizer.Replace(owner)+"~")
}

type AddRequest struct {
	Content       string            `json:"content"`
	ContainerTags []string          `json:"containerTags"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

type AddResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type SearchRequest struct {
	Query        string  `json:"q"`
	ContainerTag string  `json:"containerTag"`
	SearchMode   string  `json:"searchMode,omitempty"` // "hybrid" recommended
	Limit        int     `json:"limit,omitempty"`
	Threshold    float64 `json:"threshold,omitempty"`
	Rerank       bool    `json:"rerank,omitempty"`
}

type SearchResponse struct {
	Results []SearchResult `json:"results"`
	Timing  int            `json:"timing"`
	Total   int            `json:"total"`
}

type SearchResult struct {
	ID         string  `json:"id"`
	Memory     string  `json:"memory,omitempty"`
	Chunk      string  `json:"chunk,omitempty"`
	Similarity float64 `json:"similarity"`
}
