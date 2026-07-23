import type { Metadata } from "next";
import { JetBrains_Mono, Black_Ops_One } from "next/font/google";
import { GeistSans } from "geist/font/sans";
import "./globals.css";
import { cn } from "@/lib/utils";
import { Analytics } from "@vercel/analytics/next";
import { PostHogProvider } from "@/providers/posthog-provider";
import { ClerkThemedProvider } from "@/providers/clerk-themed-provider";
import { AppToaster } from "@/components/toaster";
import { CommandMenu } from "@/components/dashboard/command-menu";
import { ThemeProvider } from "@/components/theme-provider";

const jetbrainsMono = JetBrains_Mono({subsets:['latin'],variable:'--font-jetbrains-mono'});
const blackOpsOne = Black_Ops_One({weight:'400',subsets:['latin'],variable:'--font-black-ops-one'});

export const metadata: Metadata = {
  metadataBase: new URL("https://argus.reviews"),
  title: {
    default: "ARGUS — AI Code Review That Builds Institutional Memory",
    template: "%s | ARGUS",
  },
  description:
    "AI-powered code review that traces dependencies, remembers incidents, and simulates failures before they ship. Install in 60 seconds.",
  robots: { index: true, follow: true },
  alternates: { canonical: "https://argus.reviews" },
  openGraph: {
    type: "website",
    locale: "en_US",
    url: "https://argus.reviews",
    siteName: "ARGUS",
    title: "ARGUS — AI Code Review That Builds Institutional Memory",
    description:
      "Nothing merges unseen. AI code review that gets smarter with every PR.",
  },
  twitter: {
    card: "summary_large_image",
    title: "ARGUS — AI Code Review That Builds Institutional Memory",
    description:
      "Nothing merges unseen. AI code review that gets smarter with every PR.",
  },
  other: { "theme-color": "#1A1A1A" },
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html
      lang="en"
      style={{ colorScheme: "dark" }}
      className={cn("dark", GeistSans.variable, "font-mono", jetbrainsMono.variable, blackOpsOne.variable)}
      suppressHydrationWarning
    >
      <body className="min-h-screen bg-background font-mono antialiased">
        <ThemeProvider attribute="class">
          <ClerkThemedProvider>
            <PostHogProvider>{children}</PostHogProvider>
            <CommandMenu />
          </ClerkThemedProvider>
        </ThemeProvider>
        <AppToaster />
        <Analytics />
      </body>
    </html>
  );
}
