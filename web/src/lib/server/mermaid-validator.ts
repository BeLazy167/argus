import mermaid from "mermaid";
import mermaidPackage from "mermaid/package.json";

export const MAX_MERMAID_SOURCE_BYTES = 20_000;
export const MERMAID_VERSION = mermaidPackage.version;

type MermaidValidation = {
	valid: boolean;
	version: string;
	error?: string;
};

mermaid.initialize({ startOnLoad: false, suppressErrorRendering: true });

/** Parses diagram source with the exact Mermaid package deployed by the web service. */
export async function validateMermaidSource(source: string): Promise<MermaidValidation> {
	if (new TextEncoder().encode(source).byteLength > MAX_MERMAID_SOURCE_BYTES) {
		return { valid: false, version: MERMAID_VERSION, error: "source_too_large" };
	}
	if (source.trim() === "") {
		return { valid: false, version: MERMAID_VERSION, error: "empty_source" };
	}
	try {
		const parsed = await mermaid.parse(source, { suppressErrors: true });
		if (!parsed) return { valid: false, version: MERMAID_VERSION, error: "invalid_syntax" };
		return { valid: true, version: MERMAID_VERSION };
	} catch {
		return { valid: false, version: MERMAID_VERSION, error: "invalid_syntax" };
	}
}
