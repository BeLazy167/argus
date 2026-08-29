package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	gh "github.com/google/go-github/v68/github"
)

type permissionAuthTransport struct {
	base  http.RoundTripper
	token string
}

func (t permissionAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(clone)
}

type permissionErrorTransport struct {
	err error
}

func (t permissionErrorTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, t.err
}

func permissionTestClient(t *testing.T, server *httptest.Server, installationID int64, token string) *Client {
	t.Helper()
	httpClient := server.Client()
	httpClient.Transport = permissionAuthTransport{base: httpClient.Transport, token: token}
	client := gh.NewClient(httpClient)
	baseURL, err := url.Parse(server.URL + "/")
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	client.BaseURL = baseURL
	app := NewApp(1, nil)
	app.clients[installationID] = client
	return NewClient(app, "argus-eye")
}

func TestHasRepoWriteAccessUsesInstallationPermissionLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		permission string
		want       bool
	}{
		{permission: "read"},
		{permission: "triage"},
		{permission: "none"},
		{permission: "write", want: true},
		{permission: "maintain", want: true},
		{permission: "admin", want: true},
		{permission: "unexpected"},
		{permission: ""},
	}

	for _, tt := range tests {
		t.Run(tt.permission, func(t *testing.T) {
			t.Parallel()
			const token = "installation-token"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/repos/acme/widgets/collaborators/octocat/permission" {
					t.Errorf("request = %s %s, want permission-level endpoint", r.Method, r.URL.Path)
				}
				if got := r.Header.Get("Authorization"); got != "Bearer "+token {
					t.Errorf("Authorization = %q, want installation token", got)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"permission":%q}`, tt.permission)
			}))
			defer server.Close()

			client := permissionTestClient(t, server, 42, token)
			got, err := client.HasRepoWriteAccess(context.Background(), 42, "acme", "widgets", "octocat")
			if err != nil {
				t.Fatalf("HasRepoWriteAccess() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("HasRepoWriteAccess() = %v, want %v for %q", got, tt.want, tt.permission)
			}
		})
	}
}

func TestHasRepoWriteAccessFailsClosedOnLookupFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{name: "not found", status: http.StatusNotFound},
		{name: "authentication", status: http.StatusUnauthorized, wantErr: true},
		{name: "forbidden", status: http.StatusForbidden, wantErr: true},
		{name: "rate limit", status: http.StatusTooManyRequests, wantErr: true},
		{name: "server error", status: http.StatusBadGateway, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, http.StatusText(tt.status), tt.status)
			}))
			defer server.Close()

			client := permissionTestClient(t, server, 42, "installation-token")
			allowed, err := client.HasRepoWriteAccess(context.Background(), 42, "acme", "widgets", "octocat")
			if allowed {
				t.Fatal("HasRepoWriteAccess() allowed lookup failure")
			}
			if (err != nil) != tt.wantErr {
				t.Fatalf("HasRepoWriteAccess() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestHasRepoWriteAccessFailsClosedOnTransportError(t *testing.T) {
	t.Parallel()

	transportErr := errors.New("network unavailable")
	client := gh.NewClient(&http.Client{Transport: permissionErrorTransport{err: transportErr}})
	app := NewApp(1, nil)
	app.clients[42] = client

	allowed, err := NewClient(app, "argus-eye").HasRepoWriteAccess(
		context.Background(), 42, "acme", "widgets", "octocat",
	)
	if allowed {
		t.Fatal("HasRepoWriteAccess() allowed transport failure")
	}
	if !errors.Is(err, transportErr) {
		t.Fatalf("HasRepoWriteAccess() error = %v, want transport error", err)
	}
}

func TestHasRepoWriteAccessRejectsEmptyLoginWithoutLookup(t *testing.T) {
	t.Parallel()

	app := NewApp(1, nil)
	allowed, err := NewClient(app, "argus-eye").HasRepoWriteAccess(
		context.Background(), 42, "acme", "widgets", "",
	)
	if err != nil {
		t.Fatalf("HasRepoWriteAccess() error = %v", err)
	}
	if allowed {
		t.Fatal("HasRepoWriteAccess() allowed empty login")
	}
}
