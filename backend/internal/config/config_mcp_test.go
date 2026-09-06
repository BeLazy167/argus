package config

import "testing"

func TestValidateMCP(t *testing.T) {
	t.Parallel()
	const jwks = "https://x.clerk.accounts.dev/.well-known/jwks.json"
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"disabled needs nothing", Config{MCPEnabled: false}, false},
		{"enabled needs jwks", Config{MCPEnabled: true, ClerkIssuerURL: "https://x.clerk.accounts.dev", MCPResourceURL: "https://api.example/mcp"}, true},
		{"enabled needs issuer", Config{MCPEnabled: true, ClerkJWKSURL: jwks, MCPResourceURL: "https://api.example/mcp"}, true},
		{"issuer must be absolute http(s)", Config{MCPEnabled: true, ClerkJWKSURL: jwks, ClerkIssuerURL: "clerk.accounts.dev", MCPResourceURL: "https://api.example/mcp"}, true},
		{"issuer must carry a host", Config{MCPEnabled: true, ClerkJWKSURL: jwks, ClerkIssuerURL: "https:///path", MCPResourceURL: "https://api.example/mcp"}, true},
		{"issuer must not be another scheme", Config{MCPEnabled: true, ClerkJWKSURL: jwks, ClerkIssuerURL: "ftp://clerk.accounts.dev", MCPResourceURL: "https://api.example/mcp"}, true},
		// The JWKS URL is fetched, so a non-empty string is not enough: a bare
		// host or a path-only value only fails at the first signature check.
		{"jwks must be absolute http(s)", Config{MCPEnabled: true, ClerkJWKSURL: "x.clerk.accounts.dev/.well-known/jwks.json", ClerkIssuerURL: "https://x.clerk.accounts.dev", MCPResourceURL: "https://api.example/mcp"}, true},
		{"jwks must carry a host", Config{MCPEnabled: true, ClerkJWKSURL: "/.well-known/jwks.json", ClerkIssuerURL: "https://x.clerk.accounts.dev", MCPResourceURL: "https://api.example/mcp"}, true},
		{"jwks must not be another scheme", Config{MCPEnabled: true, ClerkJWKSURL: "file:///etc/jwks.json", ClerkIssuerURL: "https://x.clerk.accounts.dev", MCPResourceURL: "https://api.example/mcp"}, true},
		{"jwks may be plain http", Config{MCPEnabled: true, ClerkJWKSURL: "http://localhost:3000/.well-known/jwks.json", ClerkIssuerURL: "http://localhost:3000", MCPResourceURL: "https://api.example/mcp"}, false},
		{"resource url required", Config{MCPEnabled: true, ClerkJWKSURL: jwks, ClerkIssuerURL: "https://x.clerk.accounts.dev"}, true},
		{"resource url must be https", Config{MCPEnabled: true, ClerkJWKSURL: jwks, ClerkIssuerURL: "https://x.clerk.accounts.dev", MCPResourceURL: "http://api.example/mcp"}, true},
		{"resource url must end with /mcp", Config{MCPEnabled: true, ClerkJWKSURL: jwks, ClerkIssuerURL: "https://x.clerk.accounts.dev", MCPResourceURL: "https://api.example/"}, true},
		{"enabled and complete", Config{MCPEnabled: true, ClerkJWKSURL: jwks, ClerkIssuerURL: "https://x.clerk.accounts.dev", MCPResourceURL: "https://api.example/mcp"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.cfg.ValidateMCP()
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateMCP() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
