package memory

import "testing"

// TestCatalogPayloadsPassValidation is the contract the gate demanded: every
// configuration the settings card can construct from the catalog must pass
// save-time validation. The card sends, for each hosted provider entry, each
// of its models with the entry's preset base; for the custom entry, a
// user base + declared model.
func TestCatalogPayloadsPassValidation(t *testing.T) {
	for _, p := range EmbedCatalog() {
		if p.CustomModel {
			// Custom entry: keyed and keyless first-time saves both valid.
			for _, key := range []string{"k", ""} {
				req := EmbedKeyRequest{APIKey: key, BaseURL: "http://tei.internal:8080/v1", Model: "bge-m3"}
				if msg := ValidateEmbedKeyRequest(req, StoredEmbedKey{}); msg != "" {
					t.Errorf("custom (key=%q) rejected: %s", key, msg)
				}
			}
			continue
		}
		for _, m := range p.Models {
			// First-time configure with a key.
			req := EmbedKeyRequest{APIKey: "k", BaseURL: p.BaseURL, Model: m.Model}
			if msg := ValidateEmbedKeyRequest(req, StoredEmbedKey{}); msg != "" {
				t.Errorf("%s/%s first-time rejected: %s", p.Key, m.Model, msg)
			}
			// Key-preserving rotation on the SAME endpoint (the card's
			// "leave blank to keep the stored key" flow) — the bug all four
			// gate lenses confirmed must never regress.
			rot := EmbedKeyRequest{APIKey: "", BaseURL: p.BaseURL, Model: m.Model}
			storedSame := StoredEmbedKey{Found: true, HasKey: true, BaseURL: p.BaseURL}
			if msg := ValidateEmbedKeyRequest(rot, storedSame); msg != "" {
				t.Errorf("%s/%s blank-key rotation rejected: %s", p.Key, m.Model, msg)
			}
		}
	}
}

func TestValidateEmbedKeyRequestRejections(t *testing.T) {
	voyage := "https://api.voyageai.com/v1"
	cases := []struct {
		name   string
		req    EmbedKeyRequest
		stored StoredEmbedKey
	}{
		{"repo-scoped", EmbedKeyRequest{RepoScoped: true, APIKey: "k"}, StoredEmbedKey{}},
		{"base without model", EmbedKeyRequest{APIKey: "k", BaseURL: voyage}, StoredEmbedKey{}},
		{"model without base", EmbedKeyRequest{APIKey: "k", Model: "voyage-4"}, StoredEmbedKey{}},
		{"unknown hosted model", EmbedKeyRequest{APIKey: "k", BaseURL: voyage, Model: "voyage-2-deprecated"}, StoredEmbedKey{}},
		{"keyless hosted, no stored row", EmbedKeyRequest{APIKey: "", BaseURL: voyage, Model: "voyage-4"}, StoredEmbedKey{}},
		{"keyless hosted, stored keyless row", EmbedKeyRequest{APIKey: "", BaseURL: voyage, Model: "voyage-4"},
			StoredEmbedKey{Found: true, HasKey: false, BaseURL: voyage}},
		// The credential-exfiltration case: stored hosted key, blank-key
		// switch to a DIFFERENT (custom) endpoint must be rejected — the old
		// secret must never ride to a new host as a Bearer token.
		{"blank-key endpoint change carries stored secret", EmbedKeyRequest{APIKey: "", BaseURL: "http://gw.example/v1", Model: "bge-m3"},
			StoredEmbedKey{Found: true, HasKey: true, BaseURL: voyage}},
		// Hosted-to-hosted switch with blank key: same hazard, same rejection.
		{"blank-key hosted switch", EmbedKeyRequest{APIKey: "", BaseURL: "https://api.openai.com/v1", Model: "text-embedding-3-small"},
			StoredEmbedKey{Found: true, HasKey: true, BaseURL: voyage}},
		// DNS hosts are case-insensitive: a case-variant hosted base IS the
		// hosted endpoint and must hit the same guards, not classify as a
		// keyless custom endpoint that 401s at request time.
		{"case-variant hosted base, keyless", EmbedKeyRequest{APIKey: "", BaseURL: "https://API.voyageai.com/v1", Model: "voyage-4"}, StoredEmbedKey{}},
		{"keyless openrouter", EmbedKeyRequest{APIKey: "", BaseURL: "https://openrouter.ai/api/v1", Model: "voyageai/voyage-4"}, StoredEmbedKey{}},
		{"unknown openrouter model", EmbedKeyRequest{APIKey: "k", BaseURL: "https://openrouter.ai/api/v1", Model: "openai/text-embedding-3-small"}, StoredEmbedKey{}},
		{"case-variant hosted base, unknown model", EmbedKeyRequest{APIKey: "k", BaseURL: "https://Api.VoyageAI.com/v1", Model: "made-up-model"}, StoredEmbedKey{}},
		// Legacy "platform space, my key" row (key, no base): its key lives at
		// the platform endpoint, so a blank-key move to a custom endpoint is
		// still an endpoint change (exfil), not a rotation.
		{"legacy platform row to custom endpoint, blank key", EmbedKeyRequest{APIKey: "", BaseURL: "http://gw.example/v1", Model: "bge-m3"},
			StoredEmbedKey{Found: true, HasKey: true, BaseURL: ""}},
		// Grandfathering is same-endpoint only: an off-catalog model may not
		// hop providers on the strength of the old row.
		{"grandfathered model on a different base", EmbedKeyRequest{APIKey: "k", BaseURL: "https://api.openai.com/v1", Model: "voyage-code-3"},
			StoredEmbedKey{Found: true, HasKey: true, BaseURL: voyage, Model: "voyage-code-3"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if msg := ValidateEmbedKeyRequest(tc.req, tc.stored); msg == "" {
				t.Fatalf("%s: want rejection, got pass", tc.name)
			}
		})
	}
}

func TestValidateEmbedKeyRequestAllows(t *testing.T) {
	// Same-custom-endpoint rotation with a stored key: blank keeps the key.
	req := EmbedKeyRequest{APIKey: "", BaseURL: "http://tei.internal:8080/v1", Model: "bge-m4"}
	stored := StoredEmbedKey{Found: true, HasKey: true, BaseURL: "http://tei.internal:8080/v1"}
	if msg := ValidateEmbedKeyRequest(req, stored); msg != "" {
		t.Fatalf("same-endpoint custom rotation rejected: %s", msg)
	}
	// Trailing-slash tolerance on the same-base comparison.
	stored.BaseURL = "http://tei.internal:8080/v1/"
	if msg := ValidateEmbedKeyRequest(req, stored); msg != "" {
		t.Fatalf("slash-variant same-endpoint rejected: %s", msg)
	}
	voyage := "https://api.voyageai.com/v1"
	// Legacy "platform space, my key" row (key stored, base NULL): making the
	// platform config explicit via the card is a same-endpoint rotation — the
	// stored key must carry, not demand re-entry.
	legacy := StoredEmbedKey{Found: true, HasKey: true, BaseURL: ""}
	if msg := ValidateEmbedKeyRequest(EmbedKeyRequest{APIKey: "", BaseURL: voyage, Model: "voyage-4"}, legacy); msg != "" {
		t.Fatalf("legacy platform-row rotation rejected: %s", msg)
	}
	// Pre-catalog hosted model already on the row: key rotation on the same
	// endpoint must not be held hostage to a model change (= corpus re-embed).
	grand := StoredEmbedKey{Found: true, HasKey: true, BaseURL: voyage, Model: "voyage-code-3"}
	for _, key := range []string{"newkey", ""} {
		if msg := ValidateEmbedKeyRequest(EmbedKeyRequest{APIKey: key, BaseURL: voyage, Model: "voyage-code-3"}, grand); msg != "" {
			t.Fatalf("grandfathered model (key=%q) rejected: %s", key, msg)
		}
	}
	// Case-variant hosted base with a stored same-endpoint row still rotates.
	if msg := ValidateEmbedKeyRequest(EmbedKeyRequest{APIKey: "", BaseURL: "https://API.voyageai.com/v1", Model: "voyage-4"},
		StoredEmbedKey{Found: true, HasKey: true, BaseURL: voyage}); msg != "" {
		t.Fatalf("case-variant same-endpoint rotation rejected: %s", msg)
	}
}
