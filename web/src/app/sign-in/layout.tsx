import type { Metadata } from "next";
import type { ReactNode } from "react";

export const metadata: Metadata = {
  title: "Sign in — Argus",
  description:
    "Sign in to Argus. AI code review that traces dependencies, remembers incidents, and simulates failures before they ship.",
  alternates: { canonical: "https://argus.reviews/sign-in" },
};

export default function SignInLayout({ children }: { children: ReactNode }) {
  return children;
}
