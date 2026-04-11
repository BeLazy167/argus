/**
 * Docs FAQ page.
 */
export default function FAQPage() {
  return (
    <article className="space-y-8">
      <h1 className="text-2xl font-mono text-slate-100">FAQ</h1>

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
          Yes. Go to <strong className="text-slate-200">Settings → Features</strong>:
        </p>
        <ul className="list-disc pl-5 mt-2 space-y-1 text-slate-400">
          <li><strong className="text-slate-200">Issue acceptance check</strong> — toggles the issue verification worker (default: on).</li>
          <li><strong className="text-slate-200">Cross-repo PR checks</strong> — toggles the cross-PR worker (default: off).</li>
          <li><strong className="text-slate-200">Max linked PRs per review</strong> — caps how many PRs the cross-PR worker fetches (default: 5).</li>
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
