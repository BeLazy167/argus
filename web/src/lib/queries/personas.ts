import { useQueryClient } from "@tanstack/react-query";
import { createAuthMutation, createAuthQuery, getApi } from "@/lib/query-kit";
import type { Persona } from "../types";

/**
 * The list mixes two kinds of row: built-ins, which every installation shares
 * and nobody owns (`is_builtin`, no `installation_id`), and the installation's
 * own. A saved persona whose slug matches a built-in SHADOWS it — the API
 * returns the shadow, not both — which is how a team retunes "security_auditor"
 * without losing the name their repos already select.
 */
export const usePersonas = createAuthQuery<Persona[]>({
	queryKey: ["personas"],
	fetcher: (_vars, ctx) => getApi(ctx).get<Persona[]>("/api/v1/personas"),
	staleTime: 2 * 60 * 1000,
});

export type SavePersonaVars = Pick<Persona, "slug" | "name" | "prompt_overlay" | "specialist_hint">;

const useSavePersonaMutation = createAuthMutation<Persona, SavePersonaVars>({
	// PUT to the slug, not POST to the collection: saving is idempotent, and the
	// path segment is what the server treats as authoritative — so a body
	// carrying a different slug cannot rename a row out from under its own URL.
	mutationFn: ({ slug, ...body }, ctx) =>
		getApi(ctx).put<Persona>(`/api/v1/personas/${encodeURIComponent(slug)}`, { slug, ...body }),
});

export const useSavePersona = () => {
	const qc = useQueryClient();
	return useSavePersonaMutation({
		onSuccess: () => qc.invalidateQueries({ queryKey: usePersonas.getKey() }),
		onError: (err) => console.error("[save-persona] failed:", err.message),
	});
};

const useDeletePersonaMutation = createAuthMutation<unknown, string>({
	mutationFn: (slug, ctx) => getApi(ctx).delete(`/api/v1/personas/${encodeURIComponent(slug)}`),
});

/**
 * Deleting only ever removes the installation's own row. Deleting a shadow
 * reveals the built-in of the same slug again, so repos pointing at that slug
 * keep working rather than resolving to no persona.
 */
export const useDeletePersona = () => {
	const qc = useQueryClient();
	return useDeletePersonaMutation({
		onSuccess: () => qc.invalidateQueries({ queryKey: usePersonas.getKey() }),
		onError: (err) => console.error("[delete-persona] failed:", err.message),
	});
};
