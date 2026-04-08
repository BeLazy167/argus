import type { Metadata } from "next";
import { LandingContent } from "@/components/marketing/landing-content";

export const metadata: Metadata = {
  alternates: { canonical: "https://argus.reviews" },
};

export default function Page() {
  return <LandingContent />;
}
