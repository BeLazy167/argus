"use client";

import { useEffect } from "react";
import { usePathname } from "next/navigation";
import { track } from "@/lib/analytics";

/**
 * Fires `docs.page_viewed` on every docs route mount. The slug is the full
 * pathname without the leading `/docs/` prefix so reports group pages
 * intuitively even for nested docs like `/docs/features/issue-acceptance`.
 */
export function DocsPageViewedProbe() {
  const pathname = usePathname();

  useEffect(() => {
    if (!pathname) return;
    const slug = pathname.replace(/^\/docs\/?/, "") || "index";
    track("docs.page_viewed", { slug });
  }, [pathname]);

  return null;
}
