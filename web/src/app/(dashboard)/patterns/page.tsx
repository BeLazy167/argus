import { permanentRedirect } from "next/navigation";

/**
 * Patterns now lives in the unified Memory hub as a tab. This route survives
 * only as a deep link (docs, older bookmarks, and @argus-eye comment links).
 */
export default function PatternsRedirect() {
	permanentRedirect("/memory?tab=patterns");
}
