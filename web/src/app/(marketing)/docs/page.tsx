import type { Metadata } from "next";
import { DocsContent } from "@/components/marketing/docs-content";

export const metadata: Metadata = {
  title: "Documentation",
  description:
    "Complete reference for Argus AI code review: pipeline stages, code simulation, memory system, bot commands, configuration, and integration guide.",
  alternates: { canonical: "https://argus.reviews/docs" },
};

export default function DocsPage() {
  return <DocsContent />;
}
