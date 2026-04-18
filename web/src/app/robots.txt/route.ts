// Route handler (not the Metadata Routes robots.ts) because Next.js'
// MetadataRoute.Robots type has no slot for the Content-Signal header, and we
// want to declare our AI-crawler preferences alongside the User-agent rules.
//
// Content-Signal semantics (contentsignals.org):
//   ai-train=no    — do not use our content to train models
//   search=yes     — yes, index our pages for search
//   ai-input=yes   — yes, use our content as context when answering user
//                    questions (cite us in AI answers)
//
// This is the right mix for Argus: we want to appear in AI-generated answers
// (docs, "what does argus do", landing-page descriptions) but not feed the
// training corpus.

const body = `User-Agent: *
Allow: /
Disallow: /dashboard
Disallow: /sign-in
Disallow: /sign-up
Disallow: /github/

Content-Signal: ai-train=no, search=yes, ai-input=yes

Sitemap: https://argus.reviews/sitemap.xml
`;

export function GET(): Response {
  return new Response(body, {
    status: 200,
    headers: {
      "Content-Type": "text/plain; charset=utf-8",
      // Short cache — robots.txt changes are rare but we want crawlers to see
      // updates within a day of shipping, not a week.
      "Cache-Control": "public, max-age=3600, s-maxage=3600",
    },
  });
}
