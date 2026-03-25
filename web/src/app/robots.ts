import type { MetadataRoute } from "next";

export default function robots(): MetadataRoute.Robots {
  return {
    rules: [
      {
        userAgent: "*",
        allow: "/",
        disallow: ["/dashboard", "/sign-in", "/sign-up", "/github/"],
      },
    ],
    sitemap: "https://argusai.vercel.app/sitemap.xml",
  };
}
