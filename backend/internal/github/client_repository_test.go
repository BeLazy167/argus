package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	gh "github.com/google/go-github/v68/github"
)

func TestGetRepositoryMetadataUsesInstallationClient(t *testing.T) {
	var method, path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":22,"full_name":"owner/repo","default_branch":"trunk"}`))
	}))
	t.Cleanup(server.Close)

	installationClient := gh.NewClient(server.Client())
	baseURL := server.URL + "/"
	var err error
	installationClient.BaseURL, err = installationClient.BaseURL.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	app := NewApp(1, nil)
	app.clients[11] = installationClient

	metadata, err := NewClient(app, "argus").GetRepositoryMetadata(context.Background(), 11, "owner", "repo")
	if err != nil {
		t.Fatalf("get metadata: %v", err)
	}
	if method != http.MethodGet || path != "/repos/owner/repo" {
		t.Fatalf("request = %s %s", method, path)
	}
	if metadata.ID != 22 || metadata.FullName != "owner/repo" || metadata.DefaultBranch != "trunk" {
		t.Fatalf("metadata = %+v", metadata)
	}
}

func TestGetRepositoryMetadataFailsWithoutGitHubApp(t *testing.T) {
	if _, err := NewClient(nil, "argus").GetRepositoryMetadata(context.Background(), 11, "owner", "repo"); err == nil {
		t.Fatal("metadata lookup without GitHub app succeeded")
	}
}
