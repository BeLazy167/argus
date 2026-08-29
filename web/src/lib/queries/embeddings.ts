import { createAuthQuery, getApi } from "@/lib/query-kit";
import type { ProviderKey } from "../types";

export type EmbedModelOption = {
	model: string;
	notes?: string;
};

export type EmbedProviderOption = {
	key: string;
	label: string;
	base_url?: string;
	requires_key: boolean;
	models?: EmbedModelOption[];
	custom_model?: boolean;
	notes?: string;
};

/** Curated embeddings provider/model menu, served by the backend so
 * self-hosted deployments always match their backend's catalog. */
export const useEmbeddingsCatalog = createAuthQuery<{ providers: EmbedProviderOption[] }>({
	queryKey: ["embeddings-catalog"],
	fetcher: (_vars, ctx) => {
		const api = getApi(ctx);
		return api.get<{ providers: EmbedProviderOption[] }>(`/api/v1/embeddings/catalog`);
	},
	staleTime: 60 * 60 * 1000,
});

/** The installation's current embeddings BYOK row, if any (provider slot
 * "embeddings" in the provider-keys list; installation-wide by design). */
export function embeddingsRowOf(keys: ProviderKey[] | undefined): ProviderKey | undefined {
	return keys?.find((k) => k.provider === "embeddings");
}
