import { permanentRedirect } from "next/navigation";

/**
 * Scenarios now lives in the unified Memory hub as a tab. This route survives
 * only as a deep link (docs and older bookmarks point here).
 */
export default function ScenariosRedirect() {
	permanentRedirect("/memory?tab=scenarios");
}
