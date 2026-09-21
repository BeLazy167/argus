import type { Metadata } from "next";
import { LastUpdated } from "@/components/seo/last-updated";

export const metadata: Metadata = {
  title: "FAQ — Argus docs",
  description: "Frequently asked questions about Argus: triggering reviews, cost estimates, auto-review behavior, and memory.",
};

export default function FAQPage() {
  return (
    <article className="space-y-8">
      <h1 className="text-2xl font-mono text-slate-100">FAQ</h1>
      <LastUpdated date="2026-09-20" />

      <section>
        <h2 className="text-base font-mono text-slate-100 mb-2">
          Why doesn&apos;t Argus review my PR automatically?
        </h2>
        <p className="text-slate-400">
          On the hosted service, auto-review is off until you enable it — a fresh
          repo gets a one-shot{" "}
          <strong className="text-slate-200">Trigger Argus review</strong>{" "}
          checkbox with a token + cost preview instead of an automatic run
          (tick it to run on demand). Self-hosted installs review every PR by
          default and still respect an explicit off. If Argus stayed silent,
          other likely causes: the PR opened before Argus was installed, a
          webhook delivery failed, no API key is configured (you&apos;ll see an
          onboarding comment), or the base branch matches a Branch Filters skip
          pattern.
        </p>
        <p className="text-slate-400 mt-2">
          To change the default: <strong className="text-slate-200">Settings →
          Org Defaults → Auto-review</strong> (org-wide) or{" "}
          <strong className="text-slate-200">Repo Overrides → Auto-review</strong>{" "}
          (per-repo). Repo setting beats org default.
        </p>
      </section>

      <section>
        <h2 className="text-base font-mono text-slate-100 mb-2">
          The trigger checkbox doesn&apos;t appear on a PR — what now?
        </h2>
        <p className="text-slate-400">
          A few reasons the one-shot trigger comment may not be present:
        </p>
        <ul className="list-disc pl-5 mt-2 space-y-1 text-slate-400">
          <li>The PR opened before Argus was installed on the repo.</li>
          <li>A webhook delivery failed and wasn&apos;t redelivered.</li>
          <li>Auto-review is <em>on</em> — in that case no checkbox is needed.</li>
          <li>The base branch matches a skip pattern in Branch Filters.</li>
          <li>No API key is configured — you&apos;ll see an onboarding comment instead.</li>
        </ul>
        <p className="text-slate-400 mt-2">
          You can always trigger manually by commenting{" "}
          <code className="bg-slate-900 px-1 text-amber">@argus-eye review</code>{" "}
          on the PR.
        </p>
      </section>

      <section>
        <h2 className="text-base font-mono text-slate-100 mb-2">
          Who can click the trigger checkbox?
        </h2>
        <p className="text-slate-400">
          Ticking the box requires <strong className="text-slate-200">repository
          write access</strong> — Argus checks the editor&apos;s permission, not
          just the comment body. The review runs under a tighter{" "}
          <strong className="text-slate-200">10/hour per-repo</strong> cap for
          checkbox-triggered reviews (same bucket as the{" "}
          <code className="bg-slate-900 px-1 text-amber">--force</code> command flag).
          Clicks on pasted or forged trigger comments are ignored — only
          comments authored by <code className="bg-slate-900 px-1 text-amber">argus-eye[bot]</code>{" "}
          count.
        </p>
      </section>

      <section>
        <h2 className="text-base font-mono text-slate-100 mb-2">
          How many LLM tokens does the issue acceptance check cost?
        </h2>
        <p className="text-slate-400">
          One extra LLM call per linked issue, roughly 1–2k tokens (depends on diff size and
          criterion count). With 5 issues linked, budget ~5–10k extra tokens per review. Most PRs
          link zero or one issue, so the typical cost is negligible.
        </p>
      </section>

      <section>
        <h2 className="text-base font-mono text-slate-100 mb-2">
          What if the issue body has no acceptance criteria section?
        </h2>
        <p className="text-slate-400">
          Argus uses the full issue body as one free-form criterion and asks the LLM to judge the
          whole thing. You&apos;ll get one verdict instead of a per-criterion checklist. The
          verdict is usually <em>ambiguous</em> because free-form issues are hard to judge strictly
          — that&apos;s a signal to add a structured section.
        </p>
      </section>

      <section>
        <h2 className="text-base font-mono text-slate-100 mb-2">
          Why does cross-repo say &quot;no access&quot;?
        </h2>
        <p className="text-slate-400">
          Argus fetches linked PRs through the GitHub App installation of the primary PR. If the
          linked PR lives in a repo where Argus isn&apos;t installed, the GitHub API returns 404,
          and Argus logs the link as &quot;no access&quot;. The primary review still completes
          normally.
        </p>
        <p className="text-slate-400 mt-2">
          Install Argus on the linked repo to enable full cross-PR verification.
        </p>
      </section>

      <section>
        <h2 className="text-base font-mono text-slate-100 mb-2">
          Can I disable these features?
        </h2>
        <p className="text-slate-400">
          Yes. Auto-review lives under{" "}
          <strong className="text-slate-200">Settings → Org Defaults → Auto-review</strong>{" "}
          (per-repo on Repo Overrides); the verification features live under{" "}
          <strong className="text-slate-200">Settings → Org Defaults → Verification Features</strong>:
        </p>
        <ul className="list-disc pl-5 mt-2 space-y-1 text-slate-400">
          <li><strong className="text-slate-200">Auto-review</strong> — when off, opened PRs get a trigger checkbox instead of running automatically (default: off on hosted, on when self-hosted and unset).</li>
          <li><strong className="text-slate-200">Issue acceptance check</strong> — toggles the issue verification worker (default: on).</li>
          <li><strong className="text-slate-200">Cross-repo PR checks</strong> — toggles the cross-PR worker (default: on).</li>
          <li><strong className="text-slate-200">Max linked PRs per review</strong> — caps how many PRs the cross-PR worker fetches (default: 5, range 1–20).</li>
        </ul>
      </section>

      <section>
        <h2 className="text-base font-mono text-slate-100 mb-2">
          What if the linked issue is in a different repo?
        </h2>
        <p className="text-slate-400">
          Argus tries to fetch it via the installation that owns the primary PR. If that works
          (i.e., the installation has access to both repos), the check runs normally. If not, the
          link is marked &quot;no access&quot; and the review continues.
        </p>
      </section>

      <section>
        <h2 className="text-base font-mono text-slate-100 mb-2">
          Does this work with GitLab/Bitbucket?
        </h2>
        <p className="text-slate-400">
          Not yet — GraphQL{" "}
          <code className="bg-slate-900 px-1 text-amber">closingIssuesReferences</code> is a
          GitHub-only field. Other platforms would need equivalent linking APIs.
        </p>
      </section>
    </article>
  );
}
