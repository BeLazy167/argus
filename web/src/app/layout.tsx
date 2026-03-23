import type { Metadata } from "next";
import { Syne, JetBrains_Mono } from "next/font/google";
import { GeistSans } from "geist/font/sans";
import { ClerkProvider } from "@clerk/nextjs";
import { dark } from "@clerk/themes";
import "./globals.css";
import { cn } from "@/lib/utils";

const syne = Syne({
  subsets: ["latin"],
  variable: "--font-display",
  weight: ["400", "700"],
  display: "swap",
});

const jetbrainsMono = JetBrains_Mono({subsets:['latin'],variable:'--font-mono'});

export const metadata: Metadata = {
  title: "ARGUS — Nothing merges unseen",
  description:
    "AI code review that builds institutional memory. The longer it runs, the smarter it gets about your codebase.",
  metadataBase: new URL(
    process.env.NEXT_PUBLIC_APP_URL || "http://localhost:3000"
  ),
  openGraph: {
    title: "ARGUS",
    description: "Nothing merges unseen.",
    siteName: "ARGUS",
  },
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <ClerkProvider
      appearance={{
        baseTheme: dark,
        variables: {
          colorPrimary: "#F5A623",
          colorBackground: "#1A1A1A",
          colorText: "#F5F5F5",
          colorInputBackground: "#2C2C2C",
          colorInputText: "#F5F5F5",
          fontFamily: '"JetBrains Mono", monospace',
        },
      }}
    >
      <html
        lang="en"
        className={cn("dark", syne.variable, GeistSans.variable, "font-mono", jetbrainsMono.variable)}
        suppressHydrationWarning
      >
        <body className="min-h-screen bg-background font-mono antialiased">
          {children}
        </body>
      </html>
    </ClerkProvider>
  );
}
