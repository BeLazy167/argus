package memory

// EmbedKeyRequest is the provider-key payload shape relevant to embeddings
// validation (a narrow view of the API's upsert body).
type EmbedKeyRequest struct {
	RepoScoped bool
	APIKey     string
	BaseURL    string // trimmed; "" = absent
	Model      string // trimmed; "" = absent
}

// StoredEmbedKey is the existing row's state, as resolved by
// store.ResolveEmbeddingsKey. Zero value = no row. A Found row with BaseURL ""
// is the "platform space, my key" shape (key stored, platform endpoint) —
// its key lives at the platform default endpoint for rotation purposes.
type StoredEmbedKey struct {
	Found   bool
	HasKey  bool
	BaseURL string
	Model   string
}

// ValidateEmbedKeyRequest is the single authority on which embeddings
// provider-key configurations are acceptable — the API handler and the
// catalog contract tests both consume it. Returns "" when valid, else the
// user-facing rejection message.
//
// Rules (each guards a real failure mode):
//   - installation-wide only: one embedding space per installation.
//   - base_url and model travel together: a custom endpoint serves whatever
//     it serves (the declared model stamps the embedding space), and a model
//     override against the platform base would post foreign model names there.
//   - hosted models must come from the catalog: an unknown model against a
//     hosted base fails at request time, not at save time.
//   - blank api_key means "keep the stored key" ONLY for a same-endpoint
//     rotation. A hosted base with no stored key can never authenticate;
//     a stored key carried to a DIFFERENT endpoint would ship that
//     credential as a Bearer token to whatever URL the row now points at.
func ValidateEmbedKeyRequest(req EmbedKeyRequest, stored StoredEmbedKey) string {
	if req.RepoScoped {
		return "embeddings keys are installation-wide (one embedding space per installation); omit repo_id"
	}
	hasBase := req.BaseURL != ""
	if hasBase && req.Model == "" {
		return "model is required when base_url is set for embeddings (custom endpoints must declare the model they serve)"
	}
	if !hasBase && req.Model != "" {
		return "model override requires base_url (to use a different hosted model, set base_url to that provider's endpoint)"
	}
	// A stored row without a base_url is "platform space, my key": its key was
	// entered for (and only ever sent to) the platform default endpoint, so it
	// compares as that endpoint — making the config explicit via the card is a
	// same-endpoint rotation, not an endpoint change.
	storedBase := stored.BaseURL
	if stored.Found && storedBase == "" {
		storedBase = defaultVoyageBaseURL
	}
	sameBase := stored.Found && NormalizeBaseURL(storedBase) == NormalizeBaseURL(req.BaseURL)
	if hasBase && HostedKeyedBase(req.BaseURL) && !hostedModelKnown(req.BaseURL, req.Model) {
		// Grandfather a model the row already declares on the same endpoint:
		// the catalog gate exists to stop NEW unknown-model spaces, not to
		// strand a pre-catalog row where every save (e.g. a key rotation)
		// would force a model change and a full corpus re-embed.
		if !(sameBase && req.Model == stored.Model) {
			return "unknown model for this provider; pick one from the embeddings catalog"
		}
	}
	if req.APIKey == "" && hasBase {
		if HostedKeyedBase(req.BaseURL) {
			if !stored.HasKey || !sameBase {
				return "this endpoint requires an api_key (leave blank only when rotating settings on the same configured endpoint)"
			}
		} else if stored.HasKey && !sameBase {
			return "changing endpoints requires re-entering the api_key (or delete the configuration first to go keyless)"
		}
	}
	return ""
}

// hostedModelKnown reports whether model appears in the catalog entry whose
// preset base matches base. Non-hosted (custom) bases are not catalog-bound.
func hostedModelKnown(base, model string) bool {
	b := NormalizeBaseURL(base)
	for _, p := range EmbedCatalog() {
		if p.BaseURL != "" && NormalizeBaseURL(p.BaseURL) == b {
			for _, m := range p.Models {
				if m.Model == model {
					return true
				}
			}
			return false
		}
	}
	// Hosted-keyed base with no catalog entry (future-proofing): don't block.
	return true
}
