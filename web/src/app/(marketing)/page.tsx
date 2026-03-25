import type { Metadata } from "next";
import { LandingContent } from "@/components/marketing/landing-content";

export const metadata: Metadata = {
  alternates: { canonical: "https://argusai.vercel.app" },
};

export default function Page() {
  return <LandingContent />;
}
