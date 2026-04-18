import type { NextConfig } from "next";

// RFC 8288 Link headers for agent discovery. Each entry advertises a
// machine-readable companion resource using an IANA-registered (or de-facto
// standard) link relation. Kept minimal: only resources that actually exist
// and are useful to crawlers. Adding a rel= here without shipping the
// resource gives a broken pointer, so prune when deleting the target.
const AGENT_DISCOVERY_LINKS = [
  '</sitemap.xml>; rel="sitemap"; type="application/xml"',
  '</llms.txt>; rel="llms-txt"; type="text/plain"',
  '</docs>; rel="service-doc"; type="text/html"',
].join(", ");

const nextConfig: NextConfig = {
  images: {
    remotePatterns: [
      { protocol: "https", hostname: "img.clerk.com" },
      { protocol: "https", hostname: "avatars.githubusercontent.com" },
    ],
  },
  async headers() {
    return [
      {
        // Landing page only. Attaching the Link header to every route inflates
        // response size for dashboard pages that don't benefit (and may carry
        // user PII cached by shared proxies).
        source: "/",
        headers: [{ key: "Link", value: AGENT_DISCOVERY_LINKS }],
      },
    ];
  },
};

export default nextConfig;
