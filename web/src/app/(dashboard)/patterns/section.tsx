"use client";

import { Brain, Filter, Loader2, Plus, Trash2, TrendingUp } from "lucide-react";
import React, { useMemo, useState } from "react";
import {
	Area,
	AreaChart,
	CartesianGrid,
	ResponsiveContainer,
	Tooltip,
	XAxis,
	YAxis,
} from "recharts";
import {
	PaginationBar,
	usePagination,
	useSearchParamState,
	useUpdateSearchParams,
} from "@/components/dashboard/pagination";
import { useActiveRepo } from "@/lib/hooks/use-active-repo";
import {
	useCreatePattern,
	useDeletePattern,
	usePatternStats,
	usePatterns,
} from "@/lib/queries/patterns";
import { useRepos } from "@/lib/queries/repos";
import { formatDistanceToNow } from "@/lib/time";
import { useInstallation } from "@/providers/installation-provider";

type SourceFilter = "all" | "manual" | "auto_learn" | "convention" | "scoring_confirmed";

const SOURCE_LABELS: Record<string, string> = {
	manual: "Manual",
	auto_learn: "AI-Learned",
	convention: "Convention",
	scoring_confirmed: "Confirmed",
};

const SOURCE_BADGE_STYLES: Record<string, string> = {
	manual: "border-slate-500/30 bg-slate-500/10 text-slate-400",
	auto_learn: "border-amber/30 bg-amber/10 text-amber",
	convention: "border-blue-500/30 bg-blue-500/10 text-blue-400",
	scoring_confirmed: "border-emerald-500/30 bg-emerald-500/10 text-emerald-400",
};

const SOURCE_TAB_ACTIVE_STYLES: Record<SourceFilter, string> = {
	all: "border-amber/40 bg-amber/10 text-amber",
	manual: "border-slate-500/40 bg-slate-500/10 text-slate-300",
	auto_learn: "border-amber/40 bg-amber/10 text-amber",
	convention: "border-blue-500/40 bg-blue-500/10 text-blue-400",
	scoring_confirmed: "border-emerald-500/40 bg-emerald-500/10 text-emerald-400",
};

const SOURCE_TABS = ["all", "scoring_confirmed", "auto_learn", "convention", "manual"] as const;

const getSource = (p: { source?: string }) => p.source || "manual";

export function PatternsSection() {
	const { activeId } = useActiveRepo();
	const { active } = useInstallation();
	const activeRepoId = activeId || undefined;
	const {
		data: patterns,
		isLoading,
		isError: patternsError,
	} = usePatterns({ variables: { repoId: activeRepoId } });
	const { data: repos } = useRepos();
	const { data: stats } = usePatternStats({ variables: { installationId: active?.id } });
	const createPattern = useCreatePattern();
	const deletePattern = useDeletePattern();
	const [content, setContent] = useState("");
	const [selectedRepoId, setSelectedRepoId] = useState<number | undefined>();
	const [filterRepo] = useSearchParamState("repo", "all");
	const [sourceFilter] = useSearchParamState("source", "all");
	const updateParams = useUpdateSearchParams();
	const [expandedId, setExpandedId] = useState<number | null>(null);

	const repoMap = useMemo(() => {
		const m = new Map<number, string>();
		for (const r of repos ?? []) {
			m.set(r.id, r.full_name);
		}
		return m;
	}, [repos]);

	const filtered = useMemo(() => {
		if (!patterns) return [];
		const repoId = filterRepo !== "all" && filterRepo !== "org" ? Number(filterRepo) : null;
		const result: typeof patterns = [];
		for (const p of patterns) {
			const src = getSource(p);
			if (sourceFilter !== "all" && src !== sourceFilter) continue;
			if (filterRepo === "org" && p.repo_id) continue;
			if (repoId !== null && p.repo_id !== repoId) continue;
			result.push(p);
		}
		return result;
	}, [patterns, filterRepo, sourceFilter]);

	// Single-pass counts: source counts + per-repo pattern counts.
	const { sourceCounts, repoCounts } = useMemo(() => {
		const source = { all: 0, manual: 0, auto_learn: 0, convention: 0, scoring_confirmed: 0 };
		const repo = new Map<number, number>();
		if (!patterns) return { sourceCounts: source, repoCounts: repo };
		source.all = patterns.length;
		for (const p of patterns) {
			const src = getSource(p);
			if (src === "manual") source.manual++;
			else if (src === "auto_learn") source.auto_learn++;
			else if (src === "convention") source.convention++;
			else if (src === "scoring_confirmed") source.scoring_confirmed++;
			if (p.repo_id) repo.set(p.repo_id, (repo.get(p.repo_id) ?? 0) + 1);
		}
		return { sourceCounts: source, repoCounts: repo };
	}, [patterns]);

	// Transform stats for stacked area chart. Bucket by the ISO week timestamp so
	// same month/day weeks in different years don't collapse into one point; the
	// visible label only carries the year when the data spans multiple years.
	const chartData = useMemo(() => {
		if (!stats || stats.length === 0) return [];
		const spanYears = new Set(stats.map((s) => new Date(s.week).getUTCFullYear())).size > 1;
		const weekMap = new Map<
			string,
			{
				week: string;
				manual: number;
				auto_learn: number;
				convention: number;
				scoring_confirmed: number;
			}
		>();
		for (const s of stats) {
			const d = new Date(s.week);
			const bucketKey = d.toISOString();
			const label = d.toLocaleDateString("en-US", {
				month: "short",
				day: "numeric",
				...(spanYears ? { year: "2-digit" as const } : {}),
			});
			let entry = weekMap.get(bucketKey);
			if (!entry) {
				entry = { week: label, manual: 0, auto_learn: 0, convention: 0, scoring_confirmed: 0 };
				weekMap.set(bucketKey, entry);
			}
			const src =
				s.source === "auto_learn"
					? "auto_learn"
					: s.source === "convention"
						? "convention"
						: s.source === "scoring_confirmed"
							? "scoring_confirmed"
							: "manual";
			entry[src] += s.count;
		}
		return Array.from(weekMap.entries())
			.sort(([a], [b]) => a.localeCompare(b))
			.map(([, v]) => v);
	}, [stats]);

	const handleSubmit = (e: React.FormEvent) => {
		e.preventDefault();
		if (!content.trim()) return;
		createPattern.mutate({ content: content.trim(), repo_id: selectedRepoId });
		setContent("");
	};

	const { page, setPage, totalPages, paginated, pageSize, total, hasNext, hasPrev } = usePagination(
		filtered,
		undefined,
		"patternPage",
	);

	return (
		<>
			{/* Timeline Chart */}
			{chartData.length > 1 && (
				<div className="border border-iron bg-charcoal p-5 mb-8">
					<div className="flex items-center gap-2 mb-4">
						<TrendingUp className="h-4 w-4 text-slate-text" />
						<h2 className="text-xs font-mono uppercase tracking-[0.1em] text-foreground">
							Patterns Over Time
						</h2>
						<div className="flex gap-3 ml-auto">
							<span className="flex items-center gap-1.5 text-[10px] font-mono text-slate-text">
								<span className="h-2 w-2 rounded-full bg-[var(--chart-4)]" />
								Confirmed
							</span>
							<span className="flex items-center gap-1.5 text-[10px] font-mono text-slate-text">
								<span className="h-2 w-2 rounded-full bg-[var(--chart-3)]" />
								Manual
							</span>
							<span className="flex items-center gap-1.5 text-[10px] font-mono text-slate-text">
								<span className="h-2 w-2 rounded-full bg-[var(--chart-1)]" />
								AI-Learned
							</span>
							<span className="flex items-center gap-1.5 text-[10px] font-mono text-slate-text">
								<span className="h-2 w-2 rounded-full bg-[var(--chart-2)]" />
								Convention
							</span>
						</div>
					</div>
					<ResponsiveContainer width="100%" height={160}>
						<AreaChart data={chartData} margin={{ top: 0, right: 0, left: -20, bottom: 0 }}>
							<CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
							<XAxis dataKey="week" tick={{ fontSize: 10, fill: "var(--muted-foreground)" }} />
							<YAxis
								tick={{ fontSize: 10, fill: "var(--muted-foreground)" }}
								allowDecimals={false}
							/>
							<Tooltip
								contentStyle={{
									backgroundColor: "var(--card)",
									border: "1px solid var(--border)",
									borderRadius: "8px",
									fontSize: "11px",
									fontFamily: "monospace",
								}}
								labelStyle={{ color: "var(--foreground)" }}
							/>
							<Area
								type="monotone"
								dataKey="scoring_confirmed"
								name="Confirmed"
								stackId="1"
								stroke="var(--chart-4)"
								fill="var(--chart-4)"
								fillOpacity={0.4}
							/>
							<Area
								type="monotone"
								dataKey="manual"
								name="Manual"
								stackId="1"
								stroke="var(--chart-3)"
								fill="var(--chart-3)"
								fillOpacity={0.4}
							/>
							<Area
								type="monotone"
								dataKey="auto_learn"
								name="AI-Learned"
								stackId="1"
								stroke="var(--chart-1)"
								fill="var(--chart-1)"
								fillOpacity={0.4}
							/>
							<Area
								type="monotone"
								dataKey="convention"
								name="Convention"
								stackId="1"
								stroke="var(--chart-2)"
								fill="var(--chart-2)"
								fillOpacity={0.4}
							/>
						</AreaChart>
					</ResponsiveContainer>
				</div>
			)}

			{/* Add Pattern Form */}
			<form onSubmit={handleSubmit} className="mb-8">
				<div className="flex flex-col sm:flex-row gap-3">
					<input
						type="text"
						value={content}
						onChange={(e) => setContent(e.target.value)}
						placeholder="e.g. Always use guard clauses instead of nested if statements"
						className="flex-1 border border-iron bg-charcoal px-4 py-2.5 text-xs font-mono text-foreground placeholder:text-slate-text/50 focus:outline-none focus:border-amber/50 transition-colors"
					/>
					<select
						value={selectedRepoId ?? ""}
						onChange={(e) => setSelectedRepoId(e.target.value ? Number(e.target.value) : undefined)}
						style={{ backgroundColor: "var(--card)", color: "var(--foreground)" }}
						className="border border-iron bg-charcoal px-3 py-2.5 text-xs font-mono text-foreground focus:outline-none focus:border-amber/50 transition-[border-color] duration-150"
					>
						<option value="">Org-wide</option>
						{(repos ?? []).map((r) => (
							<option key={r.id} value={r.id}>
								{r.full_name.split("/").pop()}
							</option>
						))}
					</select>
					<button
						type="submit"
						disabled={!content.trim() || createPattern.isPending}
						className="flex items-center gap-2 border border-amber/30 bg-amber/10 px-4 py-2.5 text-xs font-mono font-medium text-amber hover:bg-amber/20 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
					>
						{createPattern.isPending ? (
							<Loader2 className="h-3.5 w-3.5 animate-spin" />
						) : (
							<Plus className="h-3.5 w-3.5" />
						)}
						Add
					</button>
				</div>
			</form>

			{/* Source Tabs */}
			<div className="flex items-center gap-3 mb-4">
				<div className="flex gap-1.5">
					{SOURCE_TABS.map((tab) => {
						const label = tab === "all" ? "All" : SOURCE_LABELS[tab];
						const count = sourceCounts[tab];
						const isActive = sourceFilter === tab;
						return (
							<button
								key={tab}
								type="button"
								onClick={() => updateParams({ source: tab === "all" ? "" : tab, patternPage: "" })}
								className={`rounded border px-2.5 py-1 text-[10px] font-mono transition-colors ${
									isActive
										? SOURCE_TAB_ACTIVE_STYLES[tab]
										: "border-iron text-slate-text hover:text-foreground"
								}`}
							>
								{label} ({count})
							</button>
						);
					})}
				</div>
			</div>

			{/* Repo Filter */}
			<div className="flex items-center gap-3 mb-4">
				<Filter className="h-3.5 w-3.5 text-slate-text" />
				<div className="flex gap-1.5 flex-wrap">
					<button
						type="button"
						onClick={() => updateParams({ repo: "", patternPage: "" })}
						className={`rounded border px-2.5 py-1 text-[10px] font-mono transition-colors ${
							filterRepo === "all"
								? "border-amber/40 bg-amber/10 text-amber"
								: "border-iron text-slate-text hover:text-foreground"
						}`}
					>
						All repos
					</button>
					<button
						type="button"
						onClick={() => updateParams({ repo: "org", patternPage: "" })}
						className={`rounded border px-2.5 py-1 text-[10px] font-mono transition-colors ${
							filterRepo === "org"
								? "border-purple-500/40 bg-purple-500/10 text-purple-400"
								: "border-iron text-slate-text hover:text-foreground"
						}`}
					>
						Org-wide
					</button>
					{(repos ?? []).map((r) => {
						const count = repoCounts.get(r.id) ?? 0;
						if (count === 0 && filterRepo !== String(r.id)) return null;
						return (
							<button
								key={r.id}
								type="button"
								onClick={() => updateParams({ repo: String(r.id), patternPage: "" })}
								className={`rounded border px-2.5 py-1 text-[10px] font-mono transition-colors ${
									filterRepo === String(r.id)
										? "border-blue-500/40 bg-blue-500/10 text-blue-400"
										: "border-iron text-slate-text hover:text-foreground"
								}`}
							>
								{r.full_name.split("/").pop()} ({count})
							</button>
						);
					})}
				</div>
			</div>

			{/* Patterns Table */}
			<div className="border border-iron bg-charcoal overflow-x-auto">
				<div className="flex items-center gap-2 border-b border-iron px-5 py-4">
					<Brain className="h-4 w-4 text-slate-text" />
					<h2 className="text-xs font-mono uppercase tracking-[0.1em] text-foreground">
						Active Patterns
					</h2>
					<span className="text-[10px] font-mono text-slate-text ml-auto">
						{filtered.length} patterns
					</span>
				</div>

				{isLoading ? (
					<div className="flex items-center justify-center py-10">
						<Loader2 className="h-5 w-5 animate-spin text-slate-text" />
					</div>
				) : patternsError ? (
					<div className="py-10 text-center text-xs font-mono text-red-400">
						Failed to load patterns — check your connection and retry.
					</div>
				) : filtered.length === 0 ? (
					<div className="py-10 text-center text-xs font-mono text-slate-text">
						No patterns yet. Add one above or use{" "}
						<code className="text-amber">@argus-eye remember</code> in a PR comment.
					</div>
				) : (
					<table className="w-full">
						<thead>
							<tr className="border-b border-iron/50 text-[10px] font-mono uppercase tracking-wider text-slate-text">
								<th className="text-left px-5 py-2.5 font-medium">Content</th>
								<th className="text-left px-3 py-2.5 font-medium">Source</th>
								<th className="text-left px-3 py-2.5 font-medium">Scope</th>
								<th className="text-left px-3 py-2.5 font-medium">Created</th>
								<th className="text-right px-5 py-2.5 font-medium" />
							</tr>
						</thead>
						<tbody>
							{paginated.map((pattern) => (
								<React.Fragment key={pattern.id}>
									<tr
										className="border-b border-iron/30 last:border-0 hover:bg-iron/10 transition-colors cursor-pointer"
										onClick={() => setExpandedId(expandedId === pattern.id ? null : pattern.id)}
									>
										<td className="px-5 py-3 max-w-md">
											<p
												className={`text-xs font-mono text-foreground ${expandedId === pattern.id ? "whitespace-pre-wrap" : "truncate"}`}
											>
												{pattern.content}
											</p>
											<div className="flex items-center gap-2 mt-1">
												{pattern.category && (
													<span className="inline-block border border-iron px-1.5 py-0.5 text-[9px] font-mono text-slate-text">
														{pattern.category}
													</span>
												)}
												{pattern.pr_number && (
													<span className="text-[10px] font-mono text-slate-text">
														PR #{pattern.pr_number}
													</span>
												)}
											</div>
										</td>
										<td className="px-3 py-3">
											<span
												className={`inline-block rounded border px-2 py-0.5 text-[10px] font-mono ${
													SOURCE_BADGE_STYLES[getSource(pattern)] ?? SOURCE_BADGE_STYLES.manual
												}`}
											>
												{SOURCE_LABELS[getSource(pattern)] ?? "Manual"}
											</span>
										</td>
										<td className="px-3 py-3">
											<span
												className={`inline-block rounded border px-2 py-0.5 text-[10px] font-mono ${
													pattern.repo_id
														? "border-blue-500/30 bg-blue-500/10 text-blue-400"
														: "border-purple-500/30 bg-purple-500/10 text-purple-400"
												}`}
											>
												{pattern.repo_id
													? (repoMap.get(pattern.repo_id)?.split("/").pop() ?? "repo")
													: "org"}
											</span>
										</td>
										<td className="px-3 py-3">
											<span className="text-[10px] font-mono text-slate-text">
												{formatDistanceToNow(pattern.created_at)}
											</span>
										</td>
										<td className="px-5 py-3 text-right">
											<button
												type="button"
												aria-label="Delete pattern"
												onClick={(e) => {
													e.stopPropagation();
													deletePattern.mutate(pattern.id);
												}}
												disabled={deletePattern.isPending}
												className="text-slate-text hover:text-red-400 transition-colors disabled:opacity-50"
											>
												<Trash2 className="h-3.5 w-3.5" />
											</button>
										</td>
									</tr>
									{expandedId === pattern.id && (
										<tr className="bg-iron/5">
											<td colSpan={5} className="px-5 py-4">
												<div className="space-y-3">
													<div>
														<span className="text-[10px] font-mono text-slate-text uppercase tracking-wider">
															Full Content
														</span>
														<p className="mt-1 text-xs font-mono text-foreground whitespace-pre-wrap break-words">
															{pattern.content}
														</p>
													</div>
													<div className="flex flex-wrap gap-x-6 gap-y-2 text-[10px] font-mono">
														{pattern.created_by && (
															<div>
																<span className="text-slate-text">Created by: </span>
																<span className="text-foreground">{pattern.created_by}</span>
															</div>
														)}
														{pattern.supermemory_id && (
															<div>
																<span className="text-slate-text">Indexed: </span>
																<span className="text-green-400">✓ Supermemory</span>
															</div>
														)}
														<div>
															<span className="text-slate-text">Updated: </span>
															<span className="text-foreground">
																{new Date(pattern.updated_at).toLocaleString()}
															</span>
														</div>
													</div>
												</div>
											</td>
										</tr>
									)}
								</React.Fragment>
							))}
						</tbody>
					</table>
				)}
				<PaginationBar
					page={page}
					totalPages={totalPages}
					total={total}
					pageSize={pageSize}
					hasNext={hasNext}
					hasPrev={hasPrev}
					onNext={() => setPage(page + 1)}
					onPrev={() => setPage(page - 1)}
				/>
			</div>
		</>
	);
}
