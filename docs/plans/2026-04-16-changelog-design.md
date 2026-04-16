# Argus Changelog — Design

**Date:** 2026-04-16
**Status:** Ready for implementation
**Launch target:** Monday 2026-04-20

## Goal

Weekly-rhythm changelog that (1) signals active development to prospects, (2) gives the team a truthful internal log, (3) costs ~30 min/week to maintain.

## Surfaces

Two docs, one source of truth.

- **`CHANGELOG.md`** (repo root) — team-internal log, Keep-a-Changelog format, includes `### Deploy notes` subsections, `(#PR)` suffixes on every bullet. Repo is private — audience is team + future hires, not self-hosters.
- **`web/src/content/changelog.mdx`** (public page at `/changelog`) — curated marketing surface, same bullets minus deploy-note sections, minus PR suffixes, embeds allowed (Next.js MDX). For prospects + users.

## Cadence + versioning

- Date-based headings. `## [2026-04-20]`. No semver, no calver.
- Weekly rollup. **Monday** = cut day (not Friday — Monday publish gets higher social/RSS attention).
- `## [Unreleased]` buffer holds merged-but-not-cut entries in `CHANGELOG.md`. Public page only renders dated sections, never shows Unreleased.

## Categories (5)

`Added` / `Changed` / `Removed` / `Fixed` / `Security`. Drop Deprecated (no API surface with deprecation semantics). Deploy notes in a `### Deploy notes` subsection under the week, stripped when mirrored to MDX.

## Entry format

```md
## [Unreleased]
### Added
- We added per-agent token breakdown to review summaries. (#PR)
### Fixed
- We fixed comment titles truncating mid-sentence. (#PR)
### Deploy notes
- Run migration 035_cancel_status before next deploy.
```

Voice: **first-person plural, simple past tense** ("We added X" — not "We've added" or "Added"). No emojis in bullets. One sentence per bullet default.

## Authoring pipeline

Every PR touching user-visible behavior edits `CHANGELOG.md`'s `## [Unreleased]` section in the same diff. Author writes `(#PR)` literal — Monday script replaces with real PR numbers. If PR has no user-facing impact, author writes no changelog entry. PR reviewers enforce voice on each bullet.

### PR template addition (`.github/pull_request_template.md`)

```md
## Changelog
<!-- If user-facing behavior changed, add a bullet to CHANGELOG.md's [Unreleased] section.
     Voice: "We [past-tense verb] X." Use (#PR) placeholder — script fills in the number.
     If this PR has no user-visible effect: write "N/A - internal" here. -->
```

## Monday rollup automation

**GitHub Action:** `.github/workflows/changelog-rollup.yml`
**Cron:** `0 14 * * MON` (Monday 14:00 UTC = 9am ET / 6am PT)
**Manual trigger:** `workflow_dispatch` enabled for off-cycle runs

### Script: `scripts/changelog-rollup.js` (~40 lines)

1. Read `CHANGELOG.md`, extract `[Unreleased]` block
2. Replace `(#PR)` placeholders with actual merged-PR numbers from the past 7 days
3. Rename header → `## [YYYY-MM-DD]`, insert fresh empty `[Unreleased]` above
4. Strip `(#\d+)` suffixes + `### Deploy notes` sections, prepend to `web/src/content/changelog.mdx` with a `## April 20, 2026` MDX heading
5. If `[Unreleased]` empty: exit cleanly, no PR opened
6. Open PR titled `chore: changelog rollup YYYY-MM-DD` with both files changed. PR body includes:
   - Draft tweet/BlueSky post in a `## Suggested social copy` section
   - Draft Buttondown email copy in a `## Suggested email` section
   - Checklist: `- [ ] Add hero image to web/public/changelog/YYYY-MM-DD-slug.png`
   - Checklist: `- [ ] Voice-edit the MDX for any awkward lines`

Human reviews, adds hero image, voice-edits MDX, merges. Rollup PR is **not** auto-merged.

## Distribution + discovery

- **Navbar:** add `Changelog` between `Compare` and `Docs`. Order: Pricing / Compare / Changelog / Docs / Blog.
- **RSS:** `app/(marketing)/changelog/rss.xml/route.ts` generates RSS 2.0 from the MDX. `<link rel="alternate" type="application/rss+xml">` in page head.
- **Email:** use existing Buttondown list (already wired on `/blog`). After merging weekly rollup PR, paste suggested email copy into Buttondown UI, send to list.
- **Social:** Monday rollup PR body drafts tweet + BlueSky post. Human posts after merge.
- **Dashboard pip ("✨ new"):** deferred until 100+ active installations.

## Screenshots

- One **hero image per week** at the top of each week's block. Rest of bullets text-only.
- Storage: `web/public/changelog/YYYY-MM-DD-slug.png`, served by Next/Image.
- Format: PNG for UI captures (WebP-converted by Next/Image). Reference size 1600×900, `sips -Z` compressed to <200KB.
- Motion: use `<video autoplay muted loop playsinline>` with MP4, not GIF (file size).
- **Alt text mandatory** on every image.

## Launch plan (Fri 4/17 → Mon 4/20)

**Friday 4/17:**
- Create `CHANGELOG.md` at repo root with hand-written `[2026-04-20]` section covering last week (logo rollout, per-agent token breakdown, compare matrix rework, `@argus-eye resolve` fix, marketing sanitization, reaction sweep, comment title fix, prior-dedup, cross-PR/acceptance gating).
- Delete stale `docs/CHANGELOG.md`.
- Add PR template.

**Saturday 4/18:**
- Install `@next/mdx` + `@mdx-js/loader`. Update `next.config.ts` with `pageExtensions: ['ts', 'tsx', 'md', 'mdx']`.
- Create `web/src/content/changelog.mdx` with same backfill, stripped of PR suffixes and deploy-notes.
- Create `web/src/app/(marketing)/changelog/page.tsx` route. Style: match `/docs` visual language (font-mono headers, amber accent, iron borders, void background).
- Add `Changelog` link to `web/src/components/marketing/navbar.tsx`.

**Sunday 4/19:**
- Create `web/src/app/(marketing)/changelog/rss.xml/route.ts`.
- Create `scripts/changelog-rollup.js` + `.github/workflows/changelog-rollup.yml`.
- Test with `workflow_dispatch` against empty `[Unreleased]` — expect clean skip, no PR opened.
- Take hero image for launch entry.

**Monday 4/20 (launch day):**
- Deploy to Vercel (`vercel --prod`).
- Verify `/changelog` live + RSS valid (paste URL into Feedly).
- Post one tweet + BlueSky: `Argus now has a changelog at argus.reviews/changelog — first entry covers this past week's work. Weekly rollups every Monday.`
- Paste email copy into Buttondown, send to existing subscribers.
- First `[Unreleased]` bullet of week goes in with next PR.

## Launch self-announcement

Changelog announces itself via one bullet in the `[2026-04-20]` section:

```md
### Added
- We launched /changelog. This is the first entry. (Weekly rollups every Monday.)
```

No dedicated "introducing" blog post. The concrete work in the other bullets does the real signaling.

## Files to create / modify

| File | Action |
|------|--------|
| `CHANGELOG.md` | **Create** at repo root with `[2026-04-20]` backfill |
| `docs/CHANGELOG.md` | **Delete** (stale, leaks sanitized internals) |
| `web/src/content/changelog.mdx` | **Create** with public-facing mirror of backfill |
| `web/src/app/(marketing)/changelog/page.tsx` | **Create** (renders MDX) |
| `web/src/app/(marketing)/changelog/rss.xml/route.ts` | **Create** (generates RSS 2.0) |
| `web/src/components/marketing/navbar.tsx` | **Modify** (add Changelog link between Compare and Docs) |
| `web/next.config.ts` | **Modify** (add MDX config: `pageExtensions`, `@next/mdx` plugin) |
| `web/package.json` | **Modify** (add `@next/mdx`, `@mdx-js/loader`, `@mdx-js/react`) |
| `scripts/changelog-rollup.js` | **Create** (rollup logic) |
| `.github/workflows/changelog-rollup.yml` | **Create** (Monday cron + workflow_dispatch) |
| `.github/pull_request_template.md` | **Create or modify** (add Changelog section) |

## Success metrics (revisit at day 90)

- Publish consistency: 12/13 Mondays shipped on time
- RSS subscribers > 20
- `/changelog` organic page views > 500/mo
- At least 3 inbound mentions ("read your changelog", "love the weekly cadence")

Below any of these thresholds at day 90: re-evaluate cadence or distribution.

## Open implementation details

None of these block launch — resolve during implementation or post-launch.

- PR template: opt-in via bullet vs explicit `changelog: added/changed/fixed/etc.` label for auto-categorization? (Start with opt-in; add labels if manual sorting becomes tedious.)
- Monday cron on US federal holidays: run anyway, merge Tuesday? (Yes — skipping is worse than late.)
- Buttondown email: manual paste into UI, or Buttondown API call from the rollup Action? (Manual until week 4; automate if consistent.)
- MDX styling: match `/docs` or diverge? (Match — same typography, same colors. Visual cohesion.)
- RSS content: full entry text, or summary + link? (Full text — changelog bullets are already short.)
- If repo goes public (OSS later): `CHANGELOG.md` stays as-is, just gains external readers. No redesign needed.

## Future: full-auto iteration (post-launch)

Solo-dev target: zero-writing, one-click weekly publish. Ship only after the manual flow is stable for 4+ weeks so we know what "correct" output looks like.

### V2 design (when built)

**Per-PR bullet generation (new):**
- New GH Action on `pull_request.closed && merged == true`
- Calls Anthropic API (Claude) with PR diff + title + description + labels
- Prompt includes voice examples pulled from the existing `CHANGELOG.md` entries
- If label `changelog:skip` or PR body contains `N/A - internal` → exit
- Otherwise returns `{category, bullet}`, commits to `main`'s `[Unreleased]` with `(#PR)` suffix
- Commit message: `chore: changelog bullet for #PR [skip ci]`

**Monday rollup (extends existing):**
- Same rollup logic (cut + mirror)
- PR body additions:
  - Drafted tweet + BlueSky post
  - Drafted Buttondown email
- Solo dev: Monday morning, glance at 4-6 bullets (30 sec), click merge
- Post-merge hook on `chore: changelog rollup` merge to main:
  - Posts to X via Twitter API v2
  - Posts to BlueSky via AT Protocol
  - Sends Buttondown email via API

**Required secrets in GH:**
- `ANTHROPIC_API_KEY`
- `TWITTER_API_KEY`, `TWITTER_API_SECRET`, `TWITTER_ACCESS_TOKEN`, `TWITTER_ACCESS_SECRET`
- `BLUESKY_HANDLE`, `BLUESKY_APP_PASSWORD`
- `BUTTONDOWN_API_KEY`

**Monthly cost:** ~$0.10 Anthropic + free tiers elsewhere = negligible.

**Why not V1 (zero-touch ship):** LLM bullets are ~80% good; the 20% awkward ones would ship silently. Voice quality matters — we grilled for it. 30-sec weekly glance is cheap insurance.

**Why not V3 (auto-ship after delay):** conditional human-in-loop is worst of both — if you miss the veto window, you get V1 with extra steps.

**Prerequisites before shipping V2:**
- V1 (manual) stable for 4+ weeks
- Voice conventions documented in a clear prompt
- Rollback path tested (reverting a bad bullet commit)

## Unresolved questions

(None. V1 design is implementation-ready. V2 deferred.)
