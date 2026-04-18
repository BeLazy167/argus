import { clerkMiddleware, createRouteMatcher } from "@clerk/nextjs/server";
import { NextResponse } from "next/server";

const isPublicRoute = createRouteMatcher([
  "/",
  "/pricing(.*)",
  "/compare(.*)",
  "/docs(.*)",
  "/blog(.*)",
  "/changelog(.*)",
  "/for(.*)",
  "/glossary(.*)",
  "/sign-in(.*)",
  "/sign-up(.*)",
  "/api/webhooks(.*)",
  "/robots.txt",
  "/sitemap.xml",
  "/icon.svg",
  "/opengraph-image",
  "/twitter-image",
  "/apple-icon",
]);

// Marketing pages with a hand-crafted Markdown mirror in /public. When an AI
// agent sets `Accept: text/markdown`, we rewrite the request so it gets clean
// structured text instead of the React-rendered HTML. Browsers (which never
// prefer text/markdown) never trigger the rewrite.
const MARKDOWN_MIRRORS: Record<string, string> = {
  "/": "/landing.md",
  "/docs": "/docs.md",
};

// prefersMarkdown parses RFC 7231 Accept headers and returns true when
// text/markdown strictly outranks every other media range. A browser's
// "text/html,...,*/*;q=0.8" never wins here because */* carries q=1.0
// implicitly for missing q params.
function prefersMarkdown(accept: string | null): boolean {
  if (!accept) return false;
  const entries = accept
    .split(",")
    .map((raw) => {
      const parts = raw.trim().split(";").map((s) => s.trim());
      const type = parts[0] ?? "";
      const params = parts.slice(1);
      const qParam = params.find((p) => p.startsWith("q="));
      const q = qParam ? Number.parseFloat(qParam.slice(2)) : 1.0;
      return { type: type.toLowerCase(), q: Number.isFinite(q) ? q : 1.0 };
    })
    .filter((e) => e.type.length > 0);
  const md = entries.find((e) => e.type === "text/markdown");
  if (!md) return false;
  // Strict preference only — ties go to HTML so browsers are never surprised.
  return entries.every((e) => e.type === "text/markdown" || e.q < md.q);
}

export default clerkMiddleware(async (auth, req) => {
  // Markdown content-negotiation runs first. It only triggers on the 2 mapped
  // paths, and only when Accept strictly prefers text/markdown — so browsers
  // and the auth flow below are unaffected. The rewrite short-circuits the
  // Clerk auth check since these are all already public routes.
  const mirror = MARKDOWN_MIRRORS[req.nextUrl.pathname];
  if (mirror && prefersMarkdown(req.headers.get("accept"))) {
    const url = req.nextUrl.clone();
    url.pathname = mirror;
    const response = NextResponse.rewrite(url);
    response.headers.set("Content-Type", "text/markdown; charset=utf-8");
    // Vary tells edge caches (Vercel, Cloudflare) to key on Accept so an HTML
    // visitor and a markdown agent don't share a cache slot.
    response.headers.set("Vary", "Accept");
    return response;
  }

  if (!isPublicRoute(req)) {
    await auth.protect();
  }
});

export const config = {
  matcher: [
    "/((?!_next|[^?]*\\.(?:html?|css|js(?!on)|jpe?g|webp|png|gif|svg|ttf|woff2?|ico|csv|docx?|xlsx?|zip|webmanifest)).*)",
    "/(api|trpc)(.*)",
  ],
};
