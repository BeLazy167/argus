import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Issue acceptance check — Argus docs",
  description: "Argus verifies PRs actually address linked issues. Uses GitHub's native issue-linking — no manual setup.",
};

export default function IssueAcceptancePage() {
  return (
    <article className="space-y-6">
      <h1 className="text-2xl font-mono text-slate-100">Issue acceptance check</h1>

      <p>
        When a pull request closes an issue, Argus verifies that the diff actually addresses
        what the issue asks for. No manual setup — if you use GitHub&apos;s native issue-linking,
        Argus picks it up.
      </p>

      <h2 className="text-lg font-mono text-slate-100 pt-4">How to trigger</h2>
      <p>You have three options, any of which work:</p>
      <ol className="list-decimal pl-5 space-y-2 text-slate-400">
        <li>
          <strong className="text-slate-200">PR body text</strong> — write <code className="bg-slate-900 px-1 text-amber">Closes #123</code>,{" "}
          <code className="bg-slate-900 px-1 text-amber">Fixes #123</code>, or{" "}
          <code className="bg-slate-900 px-1 text-amber">Resolves #123</code>.
          Cross-repo: <code className="bg-slate-900 px-1 text-amber">Closes owner/repo#123</code>.
        </li>
        <li>
          <strong className="text-slate-200">GitHub UI</strong> — use the &quot;Development&quot;
          panel on the right side of the PR page to link an issue. No PR description change needed.
        </li>
        <li>
          <strong className="text-slate-200">Non-closing mention</strong> —{" "}
          <code className="bg-slate-900 px-1 text-amber">Related to #123</code> or{" "}
          <code className="bg-slate-900 px-1 text-amber">refs #123</code> also triggers the check
          (catches PRs that partially address issues).
        </li>
      </ol>
      <p>
        Argus pulls the issue via GitHub&apos;s <code className="bg-slate-900 px-1 text-amber">closingIssuesReferences</code>{" "}
        GraphQL field, so any of the above works transparently.
      </p>

      <h2 className="text-lg font-mono text-slate-100 pt-4">How to structure issues</h2>
      <p>
        Argus extracts acceptance criteria from structured sections. Use one of these headings in
        your issue body:
      </p>
      <ul className="list-disc pl-5 space-y-1 text-slate-400">
        <li><code className="bg-slate-900 px-1 text-amber">## Acceptance Criteria</code></li>
        <li><code className="bg-slate-900 px-1 text-amber">## Definition of Done</code></li>
        <li><code className="bg-slate-900 px-1 text-amber">## Expected Behavior</code></li>
        <li><code className="bg-slate-900 px-1 text-amber">## Steps to Reproduce</code></li>
      </ul>
      <p>
        Under the heading, write specific criteria as a bulleted list or checklist.{" "}
        <strong className="text-slate-200">Be specific.</strong> Instead of &quot;login works&quot;,
        write &quot;POST /login returns 401 on invalid credentials&quot; — Argus can verify
        specific behavior against the diff.
      </p>
      <p>
        If no structured section is found, Argus treats the entire issue body as one free-form
        criterion.
      </p>

      <h2 className="text-lg font-mono text-slate-100 pt-4">Verdicts</h2>
      <p>Each criterion gets one of four labels:</p>
      <ul className="list-disc pl-5 space-y-1 text-slate-400">
        <li><strong className="text-green-400">addressed</strong> — the diff provably satisfies it (with a file:line citation)</li>
        <li><strong className="text-amber">partial</strong> — the diff addresses some aspects but not all</li>
        <li><strong className="text-red-400">unaddressed</strong> — the diff doesn&apos;t touch it</li>
        <li><strong className="text-slate-400">ambiguous</strong> — the criterion is too vague or the diff&apos;s intent isn&apos;t clear</li>
      </ul>

      <h2 className="text-lg font-mono text-slate-100 pt-4">Cross-repo issues</h2>
      <p>
        If the issue lives in a different repo and Argus isn&apos;t installed there, the link shows
        up as &quot;no access&quot; in the summary. The primary review still completes normally.
      </p>

      <h2 className="text-lg font-mono text-slate-100 pt-4">Disable</h2>
      <p>
        Go to <strong className="text-slate-200">Settings → Features</strong> to toggle the check off.
        It&apos;s enabled by default because it&apos;s cheap (~1-2k extra tokens per linked issue) and
        catches real bugs.
      </p>
    </article>
  );
}
