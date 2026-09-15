import { Check, ChevronDown } from "lucide-react";
import type { Metadata } from "next";
import Link from "next/link";

const FAQ_ITEMS = [
	{
		q: "What does it cost?",
		a: "Argus itself is free and open source. The only cost is your LLM provider — you connect your own key and pay them directly, so you see and control the spend. Self-host it and there is nothing else to pay.",
	},
	{
		q: "Does it work with monorepos?",
		a: "Yes. Argus reviews the diff of each PR regardless of repo structure. It triages files individually, so large PRs in monorepos still get fast, focused reviews.",
	},
	{
		q: "What data does Argus store?",
		a: "PR metadata (title, author, branch), the diff, review comments, and optionally past patterns for memory. We never store your full source code. Diffs are processed in-memory and discarded after review.",
	},
	{
		q: "Can I use my own LLM API key?",
		a: "Yes, and it is the intended setup. Configure any OpenAI-compatible provider — OpenRouter, OpenAI, Anthropic, Azure, Vercel AI Gateway and others — per pipeline stage from the Settings page.",
	},
	{
		q: "What if Argus flags something incorrectly?",
		a: "Dismiss it. Each dismissal is stored as a memory with the reason and the kind of change it came from — repeated dismissed patterns stop being posted (security findings are never suppressed). Every comment explains its reasoning, so you can disagree and move on.",
	},
	{
		q: "Are any features held back?",
		a: "No. There is no paid tier and nothing is gated. Deep review, simulation, memory and diagrams are per-repo settings you turn on, not upsells.",
	},
	{
		q: "Can I run it on my own infrastructure?",
		a: "Yes. Argus is open source and self-hostable — set SELF_HOSTED=true, point it at your own Postgres and GitHub App, and it runs entirely on your infrastructure with your keys.",
	},
];

export const metadata: Metadata = {
	title: "Pricing",
	description:
		"Open-source AI code review. Self-host it or use the hosted app — bring your own LLM key.",
	alternates: { canonical: "https://argus.reviews/pricing" },
};

function FaqItem({ q, a }: { q: string; a: string }) {
	return (
		<details className="group border border-iron bg-charcoal">
			<summary className="flex cursor-pointer items-center justify-between px-5 py-4 text-sm font-mono text-foreground hover:text-amber transition-colors list-none">
				{q}
				<ChevronDown className="h-4 w-4 text-slate-text shrink-0 transition-transform group-open:rotate-180" />
			</summary>
			<div className="px-5 pb-4 text-xs font-mono text-slate-text leading-relaxed">{a}</div>
		</details>
	);
}

export default function PricingPage() {
	return (
		<section className="mx-auto max-w-4xl px-6 py-28">
			<div className="mb-6 text-center">
				<p className="mb-3 text-[11px] font-mono uppercase tracking-[0.15em] text-amber">Pricing</p>
				<h1 className="font-display text-4xl font-bold text-foreground mb-4">
					Ship with confidence.
				</h1>
				<p className="text-sm text-slate-text">Open source. Bring your own key.</p>
			</div>

			{/* Single tier: Argus is open source and self-hostable, so there is
			    nothing to upsell. Everything the pipeline can do is on. */}
			<div className="mx-auto max-w-md">
				<div className="border border-iron bg-charcoal p-6">
					<h3 className="font-mono text-lg font-bold text-foreground mb-1">Everything</h3>
					<div className="flex items-baseline gap-1 mb-1">
						<span className="font-mono text-3xl font-bold text-foreground">$0</span>
					</div>
					<p className="text-[11px] font-mono text-slate-text mb-6">
						Open source. Self-host it, or use the hosted app.
					</p>
					<ul className="space-y-3 mb-8">
						{[
							"Unlimited repos and reviews",
							"4 specialist deep review + Pass 2",
							"Judge-scored findings, per-PR review contract",
							"Full memory — patterns, scenarios, traces",
							"Code simulation and blast-radius analysis",
							"PR diagrams (sequence + data flow)",
							"BYOK — your LLM key, your spend, your models",
						].map((f) => (
							<li key={f} className="flex items-start gap-2.5 text-xs font-mono text-ash">
								<Check className="h-3.5 w-3.5 text-amber shrink-0 mt-0.5" />
								{f}
							</li>
						))}
					</ul>
					<Link
						href="https://github.com/BeLazy167/argus"
						target="_blank"
						rel="noreferrer"
						className="block w-full border border-iron bg-iron/30 py-2.5 text-center text-xs font-mono text-foreground transition-colors hover:bg-iron/50"
					>
						Self-host on GitHub
					</Link>
				</div>
				<p className="mt-4 text-center text-[11px] font-mono text-slate-text">
					You pay your model provider directly. Argus takes no cut.
				</p>
			</div>

			{/* FAQ */}
			<div className="mt-20">
				<h2 className="font-mono text-2xl font-bold text-foreground mb-8 text-center">Questions</h2>
				<div className="space-y-2">
					{FAQ_ITEMS.map((item) => (
						<FaqItem key={item.q} q={item.q} a={item.a} />
					))}
				</div>
			</div>

			{/* FAQ JSON-LD for rich results */}
			<script
				type="application/ld+json"
				dangerouslySetInnerHTML={{
					__html: JSON.stringify({
						"@context": "https://schema.org",
						"@type": "FAQPage",
						mainEntity: FAQ_ITEMS.map((item) => ({
							"@type": "Question",
							name: item.q,
							acceptedAnswer: {
								"@type": "Answer",
								text: item.a,
							},
						})),
					}),
				}}
			/>
		</section>
	);
}
