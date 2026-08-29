package github

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	ghinstallation "github.com/bradleyfalzon/ghinstallation/v2"
	gh "github.com/google/go-github/v68/github"

	"github.com/BeLazy167/argus/backend/internal/obs"
)

// App manages GitHub App authentication and installation token generation.
type App struct {
	appID      int64
	privateKey []byte

	mu      sync.RWMutex
	clients map[int64]*gh.Client // installation_id → client
}

func NewApp(appID int64, privateKey []byte) *App {
	return &App{
		appID:      appID,
		privateKey: privateKey,
		clients:    make(map[int64]*gh.Client),
	}
}

// ClientForInstallation returns an authenticated GitHub client for the given installation.
func (a *App) ClientForInstallation(installationID int64) (result *gh.Client, err error) {
	op := beginSemantic(context.Background(), "app.ClientForInstallation", installationID, "", "", 0)
	defer func() { op.finish(err) }()
	a.mu.RLock()
	if c, ok := a.clients[installationID]; ok {
		a.mu.RUnlock()
		return c, nil
	}
	a.mu.RUnlock()

	transport, err := ghinstallation.New(http.DefaultTransport, a.appID, installationID, a.privateKey)
	if err != nil {
		return nil, fmt.Errorf("creating installation transport: %w", err)
	}

	client := gh.NewClient(&http.Client{
		Transport: obs.NewLoggingRoundTripper("github", transport),
		Timeout:   30 * time.Second,
	})

	a.mu.Lock()
	a.clients[installationID] = client
	a.mu.Unlock()

	return client, nil
}

// ListInstallationRepos lists all repositories accessible to the given installation.
func (a *App) ListInstallationRepos(ctx context.Context, installationID int64) (result []*gh.Repository, err error) {
	op := beginSemantic(ctx, "app.ListInstallationRepos", installationID, "", "", 0)
	defer func() { op.finish(err, "result_count", len(result)) }()
	client, err := a.ClientForInstallation(installationID)
	if err != nil {
		return nil, err
	}

	var allRepos []*gh.Repository
	opts := &gh.ListOptions{PerPage: 100}
	for {
		call := beginAPICall(ctx, "rest.apps.ListRepos", installationID, "", "", 0, "page", opts.Page)
		page, resp, err := client.Apps.ListRepos(ctx, opts)
		call.finish(resp, err, "result_count", func() int {
			if page == nil {
				return 0
			}
			return len(page.Repositories)
		}())
		if err != nil {
			return nil, fmt.Errorf("listing repos: %w", err)
		}
		allRepos = append(allRepos, page.Repositories...)
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return allRepos, nil
}
