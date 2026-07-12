import type { MetadataRoute } from "next";
import { competitorSlugs } from "@/lib/pseo/competitors";
import { glossarySlugs } from "@/lib/pseo/glossary";
import { personaSlugs } from "@/lib/pseo/personas";

export default function sitemap(): MetadataRoute.Sitemap {
  const baseUrl = "https://argus.reviews";

  const core: MetadataRoute.Sitemap = [
    { url: baseUrl, lastModified: new Date(), changeFrequency: "weekly", priority: 1.0 },
    { url: `${baseUrl}/docs`, lastModified: new Date(), changeFrequency: "weekly", priority: 0.9 },
    { url: `${baseUrl}/docs/faq`, lastModified: new Date(), changeFrequency: "monthly", priority: 0.6 },
    { url: `${baseUrl}/docs/features/cross-pr-checks`, lastModified: new Date(), changeFrequency: "monthly", priority: 0.6 },
    { url: `${baseUrl}/docs/features/issue-acceptance`, lastModified: new Date(), changeFrequency: "monthly", priority: 0.6 },
    { url: `${baseUrl}/docs/features/memory-tuning`, lastModified: new Date(), changeFrequency: "monthly", priority: 0.6 },
    { url: `${baseUrl}/pricing`, lastModified: new Date(), changeFrequency: "monthly", priority: 0.8 },
    { url: `${baseUrl}/blog`, lastModified: new Date(), changeFrequency: "weekly", priority: 0.7 },
    { url: `${baseUrl}/changelog`, lastModified: new Date(), changeFrequency: "weekly", priority: 0.7 },
    { url: `${baseUrl}/compare`, lastModified: new Date(), changeFrequency: "monthly", priority: 0.8 },
    { url: `${baseUrl}/glossary`, lastModified: new Date(), changeFrequency: "monthly", priority: 0.8 },
    { url: `${baseUrl}/for`, lastModified: new Date(), changeFrequency: "monthly", priority: 0.8 },
  ];

  const comparisons: MetadataRoute.Sitemap = competitorSlugs.map((slug) => ({
    url: `${baseUrl}/compare/${slug}`,
    lastModified: new Date("2025-04-11"),
    changeFrequency: "monthly" as const,
    priority: 0.7,
  }));

  const glossary: MetadataRoute.Sitemap = glossarySlugs.map((slug) => ({
    url: `${baseUrl}/glossary/${slug}`,
    lastModified: new Date("2025-04-11"),
    changeFrequency: "monthly" as const,
    priority: 0.6,
  }));

  const personaPages: MetadataRoute.Sitemap = personaSlugs.map((slug) => ({
    url: `${baseUrl}/for/${slug}`,
    lastModified: new Date("2025-04-11"),
    changeFrequency: "monthly" as const,
    priority: 0.7,
  }));

  return [...core, ...comparisons, ...glossary, ...personaPages];
}
