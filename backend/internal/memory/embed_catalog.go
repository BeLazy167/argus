package memory

// EmbedCatalog is the curated menu of embedding configurations the settings UI
// offers. Constraint: every entry must emit vectors at the storage
// dimensionality (memories.embedding vector(1024)) — natively or via a
// truncation parameter — because an installation has exactly one embedding
// space and the similarity floors are calibrated per space.
//
// Changing the active model re-embeds the corpus (the backfill sweep re-embeds
// rows WHERE embedding_model != current) and re-resolves the per-model floors;
// the UI must warn accordingly.

// EmbedModelOption is one selectable model under a provider. The model id is
// also the display label (they were always identical).
type EmbedModelOption struct {
	Model string `json:"model"`
	// Notes surfaces tradeoffs in the picker (quality tier, price).
	Notes string `json:"notes,omitempty"`
}

// EmbedProviderOption is one provider group in the picker.
type EmbedProviderOption struct {
	// Key is the stable identifier the UI submits ("voyage", "openai", "custom").
	Key   string `json:"key"`
	Label string `json:"label"`
	// BaseURL preset for hosted providers; empty for custom (user supplies it).
	BaseURL string `json:"base_url,omitempty"`
	// RequiresKey: hosted providers need an api_key; custom endpoints may be
	// keyless (self-hosted TEI/Ollama).
	RequiresKey bool               `json:"requires_key"`
	Models      []EmbedModelOption `json:"models,omitempty"`
	// CustomModel: the UI requires a free-text model name (custom endpoints
	// must declare the model they serve).
	CustomModel bool   `json:"custom_model,omitempty"`
	Notes       string `json:"notes,omitempty"`
}

// EmbedCatalog returns the curated provider/model menu. A function (not a
// package var) so callers can't mutate the shared slices.
func EmbedCatalog() []EmbedProviderOption {
	return []EmbedProviderOption{
		{
			Key:         "voyage",
			Label:       "Voyage AI",
			BaseURL:     defaultVoyageBaseURL,
			RequiresKey: true,
			Notes:       "Platform default. Native 1024-dim output.",
			Models: []EmbedModelOption{
				{Model: "voyage-4", Notes: "default — best quality/price balance"},
				{Model: "voyage-4-lite", Notes: "cheapest, near-4 quality"},
				{Model: "voyage-3.5"},
				{Model: "voyage-3.5-lite"},
			},
		},
		{
			Key:         "openai",
			Label:       "OpenAI",
			BaseURL:     defaultOpenAIBaseURL,
			RequiresKey: true,
			Notes:       "Truncates to 1024 dims via the dimensions parameter (Matryoshka).",
			Models: []EmbedModelOption{
				{Model: "text-embedding-3-small"},
				{Model: "text-embedding-3-large", Notes: "higher quality tier, 6.5x price"},
			},
		},
		{
			Key:         "openrouter",
			Label:       "OpenRouter",
			BaseURL:     defaultOpenRouterBaseURL,
			RequiresKey: true,
			// Curated to models whose DEFAULT output is 1024-dim: OpenRouter's
			// embeddings endpoint does not document `dimensions` passthrough,
			// so Matryoshka-truncated configs (e.g. text-embedding-3-* at
			// 1024) can't be trusted through it — use the direct provider
			// entries for those.
			// Spaces are keyed by the embedding_model STRING: "voyageai/voyage-4"
			// and the direct entry's "voyage-4" are the same vectors but
			// DIFFERENT space ids, so moving an existing corpus between them
			// still triggers the model-change re-embed. The notes must not
			// promise otherwise.
			Notes: "One key for many models. Model ids are OpenRouter-prefixed: switching an existing space to its direct-provider twin (or back) re-embeds the corpus.",
			Models: []EmbedModelOption{
				{Model: "voyageai/voyage-4", Notes: "platform-default model via OpenRouter (distinct space id)"},
				{Model: "voyageai/voyage-4-lite", Notes: "cheapest, near-4 quality"},
				{Model: "baai/bge-m3", Notes: "open-weights, multilingual"},
				{Model: "mistralai/mistral-embed-2312"},
			},
		},
		{
			Key:         "vercel",
			Label:       "Vercel AI Gateway",
			BaseURL:     defaultVercelGatewayBase,
			RequiresKey: true,
			// Every model listed here was checked against the live gateway and
			// returns 1024-dim vectors WITHOUT a dimensions parameter — which
			// matters because embedBatch only sends `dimensions` for
			// text-embedding-3-*, so anything else must be natively 1024 or the
			// vector(1024) INSERT fails. Do not extend this list from the
			// gateway's catalogue without measuring the width first.
			//
			// Model ids keep the gateway's creator/model form, so these are
			// DIFFERENT space ids from the direct-provider entries above even
			// where the vectors are identical — switching re-embeds the corpus.
			Notes: "One key for many models, routed through Vercel. Ids are creator/model-prefixed: switching an existing space to its direct-provider twin (or back) re-embeds the corpus.",
			Models: []EmbedModelOption{
				{Model: "voyage/voyage-4-large", Notes: "highest quality Voyage tier — native 1024-dim"},
				{Model: "voyage/voyage-4", Notes: "balanced quality/price"},
				{Model: "voyage/voyage-4-lite", Notes: "cheapest, near-4 quality"},
				{Model: "openai/text-embedding-3-large", Notes: "higher quality tier, 6.5x price"},
				{Model: "google/gemini-embedding-001"},
			},
		},
		{
			Key:         "custom",
			Label:       "Custom endpoint (OpenAI-compatible)",
			RequiresKey: false,
			CustomModel: true,
			Notes:       "Self-hosted TEI/Ollama/vLLM or any OpenAI-compatible gateway. Must serve a 1024-dim model; declare the exact model name (the endpoint serves what it serves — the declared name stamps the embedding space).",
		},
	}
}
