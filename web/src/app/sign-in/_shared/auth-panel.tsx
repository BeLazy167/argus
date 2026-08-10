"use client";

import Image from "next/image";
import Link from "next/link";
import { Brain, GitPullRequest, Key, Sparkles } from "lucide-react";
import { ShaderAnimation } from "@/components/ui/shader-lines";
import { useMediaQuery } from "@/lib/hooks/use-media-query";

const VALUE_PROPS = [
	{
		icon: GitPullRequest,
		title: "AI review on every PR",
		body: "Installs as a GitHub App in 60 seconds. Triage → 4-specialist deep review → synthesis, posted as a structured GitHub review.",
	},
	{
		icon: Brain,
		title: "Memory compounds per repo",
		body: "Every review teaches Argus patterns and risks specific to your codebase. The next review is smarter than the last.",
	},
	{
		icon: Sparkles,
		title: "50 free reviews / month",
		body: "Free forever for small teams. Pro is $19/mo per workspace — unlimited reviews, custom personas, priority support.",
	},
	{
		icon: Key,
		title: "BYOK — your key, your model",
		body: "Bring your own OpenAI, Anthropic, or OpenRouter key. Argus never trains on your code and never stores your source.",
	},
];

/** Centered Argus wordmark linking home — forgot-password's full-width header. */
export function BrandLogoHeader() {
	return (
		<div className="flex justify-center">
			<Link href="/" aria-label="Argus home" className="inline-block">
				<Image
					src="/logo-text.png"
					alt="Argus"
					width={220}
					height={160}
					priority
					sizes="170px"
					className="h-12 w-auto"
				/>
			</Link>
		</div>
	);
}

/**
 * Two-panel auth shell shared by the sign-in and sign-up pages: a left
 * shader/brand/value-props aside (lg+ only) plus a right column that hosts
 * `children` (the page's form). `eyebrow` is the small uppercase label above
 * the heading — the sole copy difference between the two pages.
 */
export function AuthShell({
	eyebrow,
	children,
}: {
	eyebrow: string;
	children: React.ReactNode;
}) {
	// Only mount the WebGL shader on lg+ — on narrow viewports the aside is
	// display:none anyway, but the component would still initialize and tick an
	// rAF loop off-screen, draining battery. Gate the mount to avoid that.
	const isLg = useMediaQuery("(min-width: 1024px)");

	return (
		<div className="grid min-h-svh lg:grid-cols-[2fr_3fr] bg-void">
			<aside className="relative hidden overflow-hidden bg-void lg:flex lg:flex-col lg:justify-between">
				{isLg && <ShaderAnimation />}
				<div className="absolute inset-0 bg-gradient-to-tr from-void/85 via-void/55 to-void/25" />

				<div className="relative z-10 flex flex-col gap-10 p-10 xl:p-14">
					<Link href="/" aria-label="Argus home" className="inline-block">
						<Image
							src="/logo-text.png"
							alt="Argus"
							width={220}
							height={160}
							priority
							sizes="170px"
							className="h-14 w-auto drop-shadow-[0_0_18px_rgba(10,6,18,0.9)]"
						/>
					</Link>
					<div className="max-w-md">
						<p className="mb-3 text-[11px] font-mono uppercase tracking-[0.18em] text-amber">
							{eyebrow}
						</p>
						<h1 className="font-display text-4xl font-bold leading-tight text-foreground">
							Review smarter,
							<br />
							not harder.
						</h1>
						<p className="mt-4 text-sm font-mono text-ash/90 leading-relaxed">
							AI code review that traces dependencies, remembers incidents, and simulates
							failures before they ship.
						</p>
					</div>
				</div>

				<ul className="relative z-10 space-y-4 p-10 xl:p-14">
					{VALUE_PROPS.map((p) => (
						<li key={p.title} className="flex items-start gap-3 max-w-md">
							<span className="mt-0.5 inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md border border-amber/40 bg-void/60 text-amber backdrop-blur-sm">
								<p.icon className="h-4 w-4" />
							</span>
							<div>
								<h2 className="text-sm font-mono text-foreground">{p.title}</h2>
								<p className="text-[12px] font-mono text-ash/80 leading-relaxed">{p.body}</p>
							</div>
						</li>
					))}
				</ul>
			</aside>

			<div className="flex flex-col gap-4 p-6 md:p-10">
				<div className="flex justify-center lg:hidden">
					<Link href="/" aria-label="Argus home" className="inline-block">
						<Image
							src="/logo-text.png"
							alt="Argus"
							width={220}
							height={160}
							priority
							sizes="170px"
							className="h-12 w-auto"
						/>
					</Link>
				</div>

				<div className="flex flex-1 flex-col items-center justify-center">
					<div className="w-full max-w-sm">{children}</div>
				</div>
			</div>
		</div>
	);
}
