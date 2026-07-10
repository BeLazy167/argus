"use client";

import { Brain, Loader2 } from "lucide-react";
import dynamic from "next/dynamic";
import { useSearchParams } from "next/navigation";
import { useState } from "react";
import { useUpdateSearchParams } from "@/components/dashboard/pagination";

// Each tab is its own chunk so opening the hub doesn't pull the heaviest
// former page bundle (react-flow + dagre live behind Architecture; recharts
// behind Patterns). Dynamic imports are deliberate here — user-approved
// exception to the no-dynamic-imports rule, per cubic review of #103.
const sectionLoading = () => (
	<div className="flex items-center justify-center py-20">
		<Loader2 className="h-6 w-6 animate-spin text-slate-text" aria-hidden />
	</div>
);
const PatternsSection = dynamic(
	() => import("../patterns/section").then((m) => m.PatternsSection),
	{ loading: sectionLoading },
);
const ScenariosSection = dynamic(
	() => import("../scenarios/section").then((m) => m.ScenariosSection),
	{ loading: sectionLoading },
);
const ArchitectureSection = dynamic(
	() => import("../architecture/section").then((m) => m.ArchitectureSection),
	{ loading: sectionLoading },
);
const InsightsSection = dynamic(
	() => import("../insights/section").then((m) => m.InsightsSection),
	{ loading: sectionLoading },
);

type MemoryTab = "patterns" | "scenarios" | "architecture" | "insights";

const TABS: { key: MemoryTab; label: string }[] = [
	{ key: "patterns", label: "Patterns" },
	{ key: "scenarios", label: "Scenarios" },
	{ key: "architecture", label: "Architecture" },
	{ key: "insights", label: "Insights" },
];

function isMemoryTab(value: string | null): value is MemoryTab {
	return (
		value === "patterns" ||
		value === "scenarios" ||
		value === "architecture" ||
		value === "insights"
	);
}

export default function MemoryPage() {
	const searchParams = useSearchParams();
	const updateParams = useUpdateSearchParams();
	// Deep link via ?tab=<x> so old routes can redirect straight to a section.
	// Read once at mount — the tab bar owns state thereafter (no effect).
	const [tab, setTab] = useState<MemoryTab>(() => {
		const t = searchParams.get("tab");
		return isMemoryTab(t) ? t : "patterns";
	});

	// Mirror the active tab into ?tab= so refresh/share reflects the viewed
	// section (router.replace, same idiom the sections use). Default tab drops
	// the param for a clean URL.
	const selectTab = (key: MemoryTab) => {
		setTab(key);
		updateParams({ tab: key === "patterns" ? "" : key });
	};

	return (
		<>
			<div className="mb-6">
				<div className="flex items-center gap-2">
					<Brain className="h-5 w-5 text-amber" />
					<h1 className="font-mono text-2xl font-bold text-foreground">Memory</h1>
				</div>
				<p className="text-xs font-mono text-slate-text mt-1">
					Everything Argus has learned about your code — and what it watches for.
				</p>
			</div>

			{/* Section tabs — mirrors the Settings page tab idiom. */}
			<div
				className="flex items-center gap-1 mb-8 border-b border-iron"
				role="tablist"
				aria-label="Memory sections"
			>
				{TABS.map(({ key, label }) => (
					<button
						key={key}
						type="button"
						role="tab"
						aria-selected={tab === key}
						onClick={() => selectTab(key)}
						className={`px-4 py-3 text-xs font-mono transition-colors border-b-2 -mb-px cursor-pointer ${
							tab === key
								? "border-amber text-amber"
								: "border-transparent text-slate-text hover:text-foreground"
						}`}
					>
						{label}
					</button>
				))}
			</div>

			{tab === "patterns" && <PatternsSection />}
			{tab === "scenarios" && <ScenariosSection />}
			{tab === "architecture" && <ArchitectureSection />}
			{tab === "insights" && <InsightsSection />}
		</>
	);
}
