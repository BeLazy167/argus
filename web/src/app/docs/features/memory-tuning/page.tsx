import type { Metadata } from "next";
import { LastUpdated } from "@/components/seo/last-updated";

export const metadata: Metadata = {
	title: "Memory tuning — Argus docs",
	description:
		"Argus remembers past code review patterns, scenarios, and developer feedback. Tune the similarity thresholds and retirement policy per org.",
};

export default function MemoryTuningPage() {
	return (
		<article className="space-y-6">
			<h1 className="text-2xl font-mono text-slate-100">Memory tuning</h1>
			<LastUpdated date="2026-09-20" />

			<p>
				Argus remembers things across reviews. Confirmed patterns, known scenarios, dismissed
				findings, per-file synthesis — all of it feeds back into future reviews as context. The
				match is semantic, not exact text: a similarity score between 0 and 1 gates whether each
				memory influences a review.
			</p>
			<p>
				Four of those gates are tunable per-org, plus one toggle that controls long-term cleanup.
				Defaults work for most teams. Tune when you observe specific failure modes in your reviews.
			</p>
			<p className="text-[12px] text-slate-500">
				Two gates only matter when the matching pipeline features are on:{" "}
				<code className="bg-slate-900 px-1 text-amber">specialist_min</code> feeds the Deep Review
				specialists, and <code className="bg-slate-900 px-1 text-amber">scenario_trigger</code>{" "}
				feeds Simulation. Both are off by default — enable them under{" "}
				<strong className="text-slate-200">Settings → Org Defaults → Pipeline Features</strong>{" "}
				before tuning their gates.
			</p>

			<h2 className="text-lg font-mono text-slate-100 pt-4">Where to tune</h2>
			<p>
				Open <strong className="text-slate-200">Settings</strong> from the dashboard sidebar and
				switch to the <strong className="text-slate-200">Memory</strong> tab. Changes apply to the
				next review. An overridden field shows an amber border and a delta chip; reset to default
				via the circular-arrow icon next to each control.
			</p>

			<h2 className="text-lg font-mono text-slate-100 pt-4">Thresholds</h2>
			<p>
				Each gate is a similarity cutoff in{" "}
				<code className="bg-slate-900 px-1 text-amber">[0, 1]</code>. Higher = stricter (fewer but
				more relevant matches). Lower = more permissive (more context, more noise). Scores are raw
				cosine similarity over the configured embedding space — they sit high, so the defaults
				are higher than intuition suggests. An explicit{" "}
				<code className="bg-slate-900 px-1 text-amber">0</code> disables that gate entirely.
			</p>

			<h3 className="text-base font-mono text-slate-100 pt-3">finding_enrich</h3>
			<p>
				Default <code className="bg-slate-900 px-1 text-amber">0.70</code>. Controls whether a
				pattern match enriches a review comment with &quot;we&apos;ve seen this before&quot;
				context. The most recall-oriented gate — a miss costs context, a false hit costs one noisy
				line.
			</p>
			<ul className="list-disc pl-5 space-y-1 text-slate-400">
				<li>
					<strong className="text-slate-200">Raise</strong> (e.g. 0.80) if you see unrelated
					patterns cited on unrelated findings — the match is too loose.
				</li>
				<li>
					<strong className="text-slate-200">Lower</strong> (e.g. 0.60) if you have a mature pattern
					library but reviews rarely cite anything — the gate is too strict.
				</li>
			</ul>

			<h3 className="text-base font-mono text-slate-100 pt-3">specialist_min</h3>
			<p>
				Default <code className="bg-slate-900 px-1 text-amber">0.80</code>. Server-side similarity
				cutoff for the <strong className="text-slate-200">deep review</strong> specialists (bug
				hunter, security, architecture, regression). Controls which patterns/scenarios/feedback they
				see per file — stricter than finding_enrich because irrelevant noise dilutes a specialist&apos;s
				prompt budget.
			</p>
			<ul className="list-disc pl-5 space-y-1 text-slate-400">
				<li>
					<strong className="text-slate-200">Raise</strong> (e.g. 0.90) if specialist prompts feel
					noisy — irrelevant past findings diluting the signal.
				</li>
				<li>
					<strong className="text-slate-200">Lower</strong> (e.g. 0.70) for small repos where the
					pattern library is still thin and you want specialists to reach further.
				</li>
			</ul>

			<h3 className="text-base font-mono text-slate-100 pt-3">scenario_trigger</h3>
			<p>
				Default <code className="bg-slate-900 px-1 text-amber">0.90</code>. When a simulation fails
				against a known scenario, this is the minimum similarity for it to count as
				&quot;triggered&quot; and bump the scenario&apos;s trigger count (used to prioritize
				long-standing issues).
			</p>
			<ul className="list-disc pl-5 space-y-1 text-slate-400">
				<li>
					<strong className="text-slate-200">Raise</strong> (e.g. 0.95) if scenarios are getting
					credit for tangential simulation failures.
				</li>
				<li>
					<strong className="text-slate-200">Lower</strong> (e.g. 0.80) if known scenarios are
					clearly related to failures but not being counted.
				</li>
			</ul>

			<h3 className="text-base font-mono text-slate-100 pt-3">scenario_dedupe</h3>
			<p>
				Default <code className="bg-slate-900 px-1 text-amber">0.95</code>. When a new candidate
				scenario is extracted, any existing scenario above this similarity counts as a duplicate and
				the new one is skipped. It sits in the top decile on purpose — genuine duplicates score
				near 1.0, and a lower bar silently merges distinct scenarios.
			</p>
			<ul className="list-disc pl-5 space-y-1 text-slate-400">
				<li>
					<strong className="text-slate-200">Raise</strong> (e.g. 0.98) if you&apos;re seeing
					distinct scenarios silently merged.
				</li>
				<li>
					<strong className="text-slate-200">Lower</strong> (e.g. 0.90) if your scenarios list has
					obvious duplicates accumulating.
				</li>
			</ul>

			<h2 className="text-lg font-mono text-slate-100 pt-4">Shared-container retirement</h2>
			<p>
				Some patterns apply across every repo in your org — conventions auto-learned from developer
				replies, for example. Those live in a shared container that, without any cleanup, would grow
				forever. One bad pattern from a single developer&apos;s reply could silently influence every
				review across every repo indefinitely.
			</p>
			<p>
				A nightly reconciler decays dormant shared patterns by age and writes the decayed confidence
				back to each doc, so a pattern fades out of reviews before it is finally deleted:
			</p>
			<ul className="list-disc pl-5 space-y-1 text-slate-400">
				<li>Day 0–30: grace window — full confidence 1.00, no decay.</li>
				<li>
					Day 30+: confidence decays <code className="bg-slate-900 px-1 text-amber">0.05</code> per
					week of dormancy, always measured from the base 1.00 (never compounded), and the
					reconciler writes the new value back to the doc each night.
				</li>
				<li>
					Below the <code className="bg-slate-900 px-1 text-amber">0.30</code> retrieval floor (~14
					weeks past grace, ≈4 months) the pattern stops influencing reviews — it is still stored
					and recoverable, just no longer surfaced.
				</li>
				<li>
					At or below <code className="bg-slate-900 px-1 text-amber">0.20</code> (~16 weeks past
					grace, ≈4.7 months of unbroken dormancy) the reconciler deletes it.
				</li>
				<li>
					Re-learning — the pipeline extracts the same pattern again — resets confidence to 1.00 and
					restarts the clock. The reconciler&apos;s own nightly write-back does not: it anchors the
					decay to the original timestamp, so aging keeps progressing until a genuine re-learn.
				</li>
			</ul>
			<p>
				<strong className="text-slate-200">disable_shared_decay</strong> — toggle this ON to keep
				everything in the shared container forever. Default OFF. Useful for regulated industries
				where pattern deletion needs a human in the loop, or for orgs early in their Argus rollout
				where you want to observe accumulated patterns before any retire.
			</p>

			<h2 className="text-lg font-mono text-slate-100 pt-4">How to know it&apos;s working</h2>
			<p>
				The <strong className="text-slate-200">scenario_trigger</strong> gate emits a structured log
				line each time it&apos;s evaluated in the pipeline:
			</p>
			<pre className="bg-slate-900 p-3 text-xs overflow-x-auto text-slate-300">
				{`INFO threshold_check name=scenario_trigger value=0.91 threshold=0.90 passed=true`}
			</pre>
			<p>
				Trace these in your observability stack after changing{" "}
				<code className="bg-slate-900 px-1 text-amber">scenario_trigger</code>. If{" "}
				<code className="bg-slate-900 px-1 text-amber">passed=true</code> on every check the
				threshold is too low; if <code className="bg-slate-900 px-1 text-amber">passed=false</code>{" "}
				on every check, too high.
			</p>
			<p>
				<code className="bg-slate-900 px-1 text-amber">finding_enrich</code> and{" "}
				<code className="bg-slate-900 px-1 text-amber">specialist_min</code> are applied server-side
				as similarity cutoffs on the memory search itself, so they don&apos;t emit a per-check log
				line. Validate those by watching review output — how many past patterns and findings get
				cited — rather than by grepping logs.
			</p>

			<h2 className="text-lg font-mono text-slate-100 pt-4">Safe defaults</h2>
			<p>
				If you&apos;re unsure, don&apos;t tune. The defaults are calibrated against measured
				similarity distributions on real review corpora. Start with defaults, observe for 2–3
				weeks, then tune only the specific gate that&apos;s misfiring.
			</p>
			<p>
				The dashboard sets these per-installation — all repos under one installation share the same
				gates. The repo-level settings schema accepts the same keys if you need a per-repo override
				via the API.
			</p>
		</article>
	);
}
