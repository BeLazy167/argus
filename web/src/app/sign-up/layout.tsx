import type { Metadata } from "next";
import type { ReactNode } from "react";

export const metadata: Metadata = {
  title: "Sign up — Argus",
  description:
    "Create an Argus account. AI code review that remembers past incidents, traces dependencies, and simulates failures before they ship. 50 free reviews/month.",
  alternates: { canonical: "https://argus.reviews/sign-up" },
};

export default function SignUpLayout({ children }: { children: ReactNode }) {
  return children;
}
