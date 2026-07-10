import { redirect } from "next/navigation";

/**
 * Memory tuning lives in the Settings page's Memory tab. This route survives
 * only as a deep link (docs and older bookmarks point here).
 */
export default function MemorySettingsRedirect() {
	redirect("/settings?tab=memory");
}
