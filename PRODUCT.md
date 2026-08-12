# Product

## Register

brand

## Platform

web

## Users

Two audiences use Argus in roughly equal measure, and the product speaks to both without a clear primary. The first is the developer whose pull request gets reviewed — they read Argus's inline comments and the review viewer, triage findings, push fixes, and want current, trustworthy feedback on their latest code. The second is the maintainer or engineering lead who installs the GitHub App, configures providers, rules, and models, tunes auto-review per repo, and watches the stats and memory that accumulate over time. The marketing site addresses the person deciding whether to adopt Argus (often the maintainer); the dashboard serves both once they're in.

## Product Purpose

Argus is an AI code reviewer that behaves like a senior engineer rather than a comment bot: it calibrates how hard to look at each pull request, backs every finding with a concrete failure scenario and a fix, verifies whether prior findings were actually addressed before resolving them, and stays silent when there is nothing worth saying. It is self-hostable and bring-your-own-key, and every review shows its work. Success is a review the reader trusts enough to act on without second-guessing — and a review that is visibly better this month than last, because the product remembers.

## Positioning

The reviewer that remembers. Argus builds institutional memory across reviews — it learns a team's prior fixes, dismissals, patterns, and rules, so its reviews compound over time instead of repeating themselves. Every screen should reinforce that the tool is accumulating judgment about *your* codebase, not running the same generic pass on every PR.

## Conversion & proof

The primary call to action is installing the GitHub App — adding Argus to your repositories on the hosted service. The secondary fallback, for visitors not ready to install or who care about privacy and control, is viewing the project on GitHub and self-hosting it (AGPL, bring-your-own-key). The line a visitor should remember after ten seconds is *the reviewer that remembers.*

Belief ladder — what a visitor must come to believe, in order, before installing: first, that AI code review can carry real judgment rather than dumping noise; then, that Argus specifically has that judgment — calibrated depth, evidence-demanding findings, verified resolution; then, that it gets better for *their* team over time because it remembers their fixes and dismissals; then, that they can trust and control it, because it shows its work, is self-hostable, and is open source; and finally, that trying it is low-risk and quick — a GitHub App install with a free tier and opt-out any time.

Proof on hand: Argus reviews its own pull requests (the project is developed under its own review gate — genuine dogfooding), and the entire codebase is open source under AGPL, so the claims are inspectable rather than asserted. There are no customer testimonials or logos supplied yet; GitHub adoption and the public review history are the social proof to lean on until named references exist. New proof (testimonials, case studies, partner logos) should be added under `.impeccable/assets/proof/` and referenced by path.

## Brand Personality

Watchful, rigorous, precise. The name is the tell: Argus, the all-seeing giant — vigilance, evidence, exactness. The voice is engineer-to-engineer, dry and confident, never hyped; it makes specific claims and shows the proof, and it treats silence as a valid answer the same way the product does. Watchful without being alarmist, exact without being cold.

## Anti-references

Do not look like a generic AI-SaaS template: no cream or warm-neutral background, no gradient-text headings, no big-number hero-metric block, no endless identical icon-card grids, no tiny uppercase tracked eyebrow above every section. Do not drift playful, consumer, or over-rounded — no mascots, bubbly shapes, or bright pastels that undercut the serious technical register. And do not become a light-mode corporate admin dashboard with a white background, blue accents, and dense chrome. The existing dark identity is deliberate and should be preserved and sharpened, not reinvented: near-black "void" surfaces, an amber accent, a stencil display face over monospace headings — a terminal, surveillance-room feel earned by the product's watchfulness.

## Design Principles

Show the work. The product's Glass Box — every review exposes its contract, what it checked, what it suppressed, and its cost — is the brand's core promise; the interface and the marketing should practice that same transparency, preferring inspectable specifics to claims.

Memory compounds. Because the positioning is institutional memory, surfaces should make prior context visible — that a review builds on earlier ones, that findings carry history — rather than presenting each screen as a fresh, stateless pass.

Evidence over noise. Mirror the product's own review laws: make specific claims with proof, cut filler, and let silence stand. No hype, no decoration that isn't carrying information.

Engineer-to-engineer. The audience is technical; credibility comes from precision and real detail, not marketing gloss. Speak plainly, name mechanisms, show real output.

Watchful restraint. The Argus identity is vigilant and exact but calm — present without shouting. Intensity is carried by typography, the amber accent, and real content, not by loud effects.

## Accessibility & Inclusion

Target WCAG 2.2 AA. Contrast is the load-bearing requirement given the dark-first palette: body text must clear 4.5:1 against its surface and large text 3:1, and the amber accent must be verified against void and charcoal backgrounds rather than assumed. Full keyboard navigation with visible focus, and `prefers-reduced-motion` honored on every animation with a crossfade or instant fallback. Do not rely on the amber accent alone to convey state — pair it with text or shape for color-blind readers.
