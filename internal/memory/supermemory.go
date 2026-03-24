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

// doRequest sends a request with the given method/path and decodes the response.
func (c *Client) doRequest(ctx context.Context, method, path string, reqBody, result any) error {
	var bodyReader io.Reader
	if reqBody != nil {
		body, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("marshaling request: %w", err)
		}
		bodyReader = bytes.NewReader(body)
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, baseURL+path, bodyReader)
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
		return fmt.Errorf("supermemory %s %s error (status %d): %s", method, path, resp.StatusCode, string(respBody))
	}

	if result != nil {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("unmarshaling response: %w", err)
		}
	}
	return nil
}

// doJSON is a convenience wrapper that calls doRequest with POST method.
func (c *Client) doJSON(ctx context.Context, path string, reqBody, result any) error {
	return c.doRequest(ctx, "POST", path, reqBody, result)
}

// AddMemory stores a new memory via v3/documents. Supports customId upserts but documents
// are queued for processing (not immediately searchable).
func (c *Client) AddMemory(ctx context.Context, req AddRequest) (*AddResponse, error) {
	var result AddResponse
	if err := c.doJSON(ctx, "/v3/documents", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// AddMemoryImmediate stores memories via v4/memories. Immediately searchable (embeddings
// generated on creation) but does NOT support customId upserts.
func (c *Client) AddMemoryImmediate(ctx context.Context, req AddImmediateRequest) (*AddImmediateResponse, error) {
	var result AddImmediateResponse
	if err := c.doJSON(ctx, "/v4/memories", req, &result); err != nil {
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

// DeleteMemory removes a document by ID.
func (c *Client) DeleteMemory(ctx context.Context, documentID string) error {
	return c.doRequest(ctx, "DELETE", "/v3/documents/"+documentID, nil, nil)
}

// ListDocuments lists documents filtered by containerTags with pagination.
func (c *Client) ListDocuments(ctx context.Context, req ListRequest) (*ListResponse, error) {
	var result ListResponse
	if err := c.doJSON(ctx, "/v3/documents/list", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// BulkDelete removes multiple documents by IDs or containerTags.
// At least one of IDs or ContainerTags must be non-empty.
func (c *Client) BulkDelete(ctx context.Context, req BulkDeleteRequest) error {
	if len(req.IDs) == 0 && len(req.ContainerTags) == 0 {
		return fmt.Errorf("BulkDelete: at least one of IDs or ContainerTags required")
	}
	return c.doRequest(ctx, "DELETE", "/v3/documents/bulk", req, &struct{}{})
}

// GetDocument retrieves a single document by ID.
func (c *Client) GetDocument(ctx context.Context, documentID string) (*Document, error) {
	var result Document
	if err := c.doRequest(ctx, "GET", "/v3/documents/"+documentID, nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
}

// tagSanitizer replaces chars invalid in Supermemory container tags.
var tagSanitizer = strings.NewReplacer(":", "-", "/", "-", "~", "-")

// OwnerTag returns a container tag scoped to an owner (user or org).
func OwnerTag(owner, kind string) string {
	return tagSanitizer.Replace(owner) + "--" + kind
}

// RepoTag returns a container tag scoped to a specific repo under an owner.
func RepoTag(owner, repo, kind string) string {
	return tagSanitizer.Replace(owner) + "--" + tagSanitizer.Replace(repo) + "--" + kind
}

// ValidateTagScope checks that a container tag belongs to the given owner.
func ValidateTagScope(tag, owner string) bool {
	return strings.HasPrefix(tag, tagSanitizer.Replace(owner)+"--")
}

type AddRequest struct {
	Content       string            `json:"content"`
	CustomID      string            `json:"customId,omitempty"`
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

// Content returns the best-available text from a search result,
// preferring Memory over Chunk.
func (r SearchResult) Content() string {
	if r.Memory != "" {
		return r.Memory
	}
	return r.Chunk
}

type ListRequest struct {
	Limit         int      `json:"limit,omitempty"`
	Page          int      `json:"page,omitempty"`
	ContainerTags []string `json:"containerTags,omitempty"`
	Sort          string   `json:"sort,omitempty"`
	Order         string   `json:"order,omitempty"`
}

type ListResponse struct {
	Memories []Document `json:"memories"`
}

type Document struct {
	ID     string `json:"id"`
	Title  string `json:"title,omitempty"`
	Status string `json:"status,omitempty"`
}

type AddImmediateRequest struct {
	ContainerTag string             `json:"containerTag"`
	Memories     []ImmediateMemory  `json:"memories"`
}

type ImmediateMemory struct {
	Content  string            `json:"content"`
	IsStatic bool              `json:"isStatic,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type AddImmediateResponse struct {
	DocumentID string `json:"documentId"`
	Memories   []struct {
		ID        string `json:"id"`
		Memory    string `json:"memory"`
		IsStatic  bool   `json:"isStatic"`
		CreatedAt string `json:"createdAt"`
	} `json:"memories"`
}

type BulkDeleteRequest struct {
	IDs           []string `json:"ids,omitempty"`
	ContainerTags []string `json:"containerTags,omitempty"`
}
