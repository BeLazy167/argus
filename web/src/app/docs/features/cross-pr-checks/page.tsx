import type { Metadata } from "next";
import { LastUpdated } from "@/components/seo/last-updated";

export const metadata: Metadata = {
  title: "Cross-repo PR compatibility — Argus docs",
  description: "Detect linked PRs across repositories and run compatibility checks. No manifest, no YAML.",
};

export default function CrossPRChecksPage() {
  return (
    <article className="space-y-6">
      <h1 className="text-2xl font-mono text-slate-100">Cross-repo PR compatibility</h1>
      <LastUpdated date="2026-04-17" />

      <p>
        Cross-repo dependencies are a known GitHub gap — there&apos;s no native way to say &quot;my
        frontend PR depends on this backend PR in another repo&quot;. Argus closes this gap by
        detecting linked PRs and running a compatibility check.
      </p>

      <h2 className="text-lg font-mono text-slate-100 pt-4">How to trigger</h2>
      <p>
        Paste a GitHub PR URL into your PR body, or use the shorthand{" "}
        <code className="bg-slate-900 px-1 text-amber">owner/repo#N</code>. Anywhere in the
        description is fine. No manifest, no YAML.
      </p>
      <pre className="bg-slate-900 p-4 overflow-x-auto text-xs text-slate-300 border border-slate-800">
{`## Context
This PR updates our frontend to consume the new API
from backend-team/api#142.

It also depends on infra-team/config#88 for the new
feature flag.`}
      </pre>

      <h2 className="text-lg font-mono text-slate-100 pt-4">What Argus does</h2>
      <ol className="list-decimal pl-5 space-y-1 text-slate-400">
        <li>Finds all GitHub PR URLs and <code className="bg-slate-900 px-1 text-amber">owner/repo#N</code> references in the PR body (up to 5 by default).</li>
        <li>After the primary review completes, runs two async stages in parallel:
          <ul className="list-disc pl-5 pt-1 space-y-1">
            <li><strong className="text-slate-200">Combination-risk judge</strong> — hydrates each linked PR&apos;s diff + prior findings from their Argus review (if any), asks the LLM to probe 9 categories: schema/migration race, serialization contract drift, type/interface drift, config contradiction, deployment ordering, security posture, enum exhaustiveness, locale/temporal, and propagated findings.</li>
            <li><strong className="text-slate-200">Joint issue coverage</strong> — when 2+ linked PRs share a referenced issue, judges whether the <em>combined</em> change addresses each acceptance criterion with per-criterion evidence (file:line).</li>
          </ul>
        </li>
        <li>Edits the sticky review comment in place, adding <strong className="text-slate-200">Cross-Repo PR Coverage</strong> and, if applicable, <strong className="text-slate-200">Joint Issue Coverage</strong> sections.</li>
        <li>When a linked PR&apos;s review completes later, re-runs only the cross-PR stage on already-reviewed PRs so late-arriving siblings refresh earlier PRs&apos; sections.</li>
      </ol>

      <h2 className="text-lg font-mono text-slate-100 pt-4">Inaccessible repos</h2>
      <p>
        If a linked repo doesn&apos;t have Argus installed, the compatibility check skips it and
        notes <em>&quot;not reviewed by Argus — diff context only&quot;</em>. The review still completes.
        Install Argus on the linked repo to enable full finding propagation.
      </p>

      <h2 className="text-lg font-mono text-slate-100 pt-4">Concurrent reviews</h2>
      <p>
        When linked PRs are reviewed at the same time, each initial cross-PR pass may run with
        partial data (siblings still reviewing). The event-driven refresh catches this: as each
        sibling completes, Argus re-runs the cross-PR stage on every already-reviewed PR that
        links to it, so the final state converges to full coverage across the family.
      </p>

      <h2 className="text-lg font-mono text-slate-100 pt-4">Default on</h2>
      <p>
        Cross-repo PR checks are enabled by default for new installations. Cost is 1–5 LLM calls
        per review depending on how many linked PRs and shared issues are involved; bounded by a
        per-install rate limit (30/hour) and a per-PR refresh cap (2 per 10 minutes). Disable in{" "}
        <strong className="text-slate-200">Settings → Features</strong>. Existing installations
        keep whatever toggle value was stored before the default flip.
      </p>

      <h2 className="text-lg font-mono text-slate-100 pt-4">Severity policy</h2>
      <p>
        Combination risks and joint-coverage gaps are reported{" "}
        <strong className="text-slate-200">informationally</strong> in the sticky comment. They
        don&apos;t bump finding severity or block a merge — the reviewer has full context to decide.
      </p>
    </article>
  );
}
