/**
 * Docs page: Cross-repo PR compatibility feature.
 */
export default function CrossPRChecksPage() {
  return (
    <article className="space-y-6">
      <h1 className="text-2xl font-mono text-slate-100">Cross-repo PR compatibility</h1>

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
        <li>Tries to fetch each linked PR&apos;s diff via its installation.</li>
        <li>Sends the primary diff + each accessible linked diff to a compatibility-judge LLM.</li>
        <li>Adds a <strong className="text-slate-200">Cross-Repo PR Coverage</strong> section to the review summary.</li>
      </ol>

      <h2 className="text-lg font-mono text-slate-100 pt-4">Inaccessible repos</h2>
      <p>
        If a linked repo doesn&apos;t have Argus installed, the compatibility check skips it and
        notes <em>&quot;no access — partial coverage&quot;</em>. The review still completes. The
        rest of the chain (accessible repos) still gets verified.
      </p>

      <h2 className="text-lg font-mono text-slate-100 pt-4">Concurrent reviews</h2>
      <p>
        When both linked PRs are being reviewed at the same time (webhooks fire in parallel),
        each review reads the other&apos;s <em>current head snapshot</em> independently. The two
        reviews may see slightly different snapshots if one is mid-flight, but pushes re-trigger
        both reviews, so the final state is always consistent.
      </p>

      <h2 className="text-lg font-mono text-slate-100 pt-4">Opt-in</h2>
      <p>
        Cross-repo PR checks cost one extra LLM call per review. They&apos;re disabled by default —
        enable them in <strong className="text-slate-200">Settings → Features</strong>.
      </p>

      <h2 className="text-lg font-mono text-slate-100 pt-4">Severity policy</h2>
      <p>
        Incompatibilities are reported <strong className="text-slate-200">informationally</strong>{" "}
        in the review summary. They don&apos;t bump finding severity or block a merge — the
        reviewer has full context to decide.
      </p>
    </article>
  );
}
