"use client";

import { Database, Loader2, Search } from "lucide-react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { PaginationBar, useUpdateSearchParams } from "@/components/dashboard/pagination";
import { useActiveRepo } from "@/lib/hooks/use-active-repo";
import { type MemoryRecord, type MemoryScope, useMemories } from "@/lib/queries/memories";
import { formatDistanceToNow } from "@/lib/time";
import { useInstallation } from "@/providers/installation-provider";

const PAGE_SIZE = 25;
const MEMORY_TYPES = [
	"pattern",
	"scenario",
	"trace",
	"feedback",
	"synthesis",
	"pr_summary",
	"review",
	"topology",
	"rule",
] as const;

function scopeFromParam(value: string | null): MemoryScope {
	if (value === "shared" || value === "repo") return value;
	return "all";
}

function pageFromParam(value: string | null): number {
	const parsed = Number(value);
	return Number.isInteger(parsed) && parsed > 0 ? parsed - 1 : 0;
}

function memoryTypeLabel(value: string): string {
	return value.replaceAll("_", " ");
}

function MemoryCard({ memory }: { memory: MemoryRecord }) {
	const scope = memory.container_tag === "_shared" ? "Shared" : memory.container_tag;
	return (
		<article className="border-b border-iron/60 px-5 py-4 last:border-0">
			<div className="mb-2 flex flex-wrap items-center gap-2">
				<span className="border border-amber/25 bg-amber/10 px-2 py-0.5 font-mono text-[10px] uppercase tracking-wider text-amber">
					{memoryTypeLabel(memory.type)}
				</span>
				<span className="border border-iron bg-iron/20 px-2 py-0.5 font-mono text-[10px] text-slate-text">
					{scope}
				</span>
				<span className="ml-auto font-mono text-[10px] text-slate-text">
					Updated {formatDistanceToNow(memory.updated_at)}
				</span>
			</div>
			<p className="whitespace-pre-wrap break-words font-mono text-xs leading-relaxed text-foreground">
				{memory.content || "// Empty memory content"}
			</p>
			<div className="mt-3 flex items-center gap-3 font-mono text-[10px] text-slate-text">
				<span className="truncate" title={memory.custom_id}>
					{memory.custom_id}
				</span>
				{memory.review_id ? (
					<Link
						className="ml-auto shrink-0 text-amber hover:underline"
						href={`/reviews/${memory.review_id}`}
					>
						Source review
					</Link>
				) : null}
			</div>
		</article>
	);
}

export function MemoriesSection() {
	const searchParams = useSearchParams();
	const updateParams = useUpdateSearchParams();
	const { active } = useInstallation();
	const { activeId } = useActiveRepo();
	const query = searchParams.get("memory_q") ?? "";
	const type = searchParams.get("memory_type") ?? "";
	const scope = scopeFromParam(searchParams.get("memory_scope"));
	const page = pageFromParam(searchParams.get("memory_page"));
	const offset = page * PAGE_SIZE;

	const { data, isLoading, error } = useMemories({
		variables: {
			installationId: active?.id ?? 0,
			repoId: activeId > 0 ? activeId : undefined,
			scope,
			type,
			query,
			limit: PAGE_SIZE,
			offset,
		},
		enabled: Boolean(active),
	});

	const total = data?.total ?? 0;
	const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));
	const hasNext = offset + (data?.memories.length ?? 0) < total;
	const hasPrev = page > 0;
	const updateFilter = (updates: Record<string, string>) =>
		updateParams({ ...updates, memory_page: "" });

	return (
		<section aria-labelledby="memories-heading">
			<div className="mb-5 flex flex-col gap-3 lg:flex-row lg:items-end lg:justify-between">
				<div>
					<h2 id="memories-heading" className="font-mono text-sm font-semibold text-foreground">
						Stored memories
					</h2>
					<p className="mt-1 font-mono text-[11px] text-slate-text">
						Live knowledge available to future reviews. The active repository filter is applied.
					</p>
				</div>
				<div className="flex flex-col gap-2 sm:flex-row">
					<label className="relative">
						<span className="sr-only">Search memories</span>
						<Search className="pointer-events-none absolute left-3 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-slate-text" />
						<input
							type="search"
							value={query}
							onChange={(event) => updateFilter({ memory_q: event.target.value })}
							placeholder="Search memory content"
							className="w-full border border-iron bg-charcoal py-2 pl-9 pr-3 font-mono text-xs text-foreground outline-none placeholder:text-slate-text focus:border-amber sm:w-64"
						/>
					</label>
					<select
						aria-label="Filter memories by scope"
						value={scope}
						onChange={(event) =>
							updateFilter({ memory_scope: event.target.value === "all" ? "" : event.target.value })
						}
						className="border border-iron bg-charcoal px-3 py-2 font-mono text-xs text-foreground outline-none focus:border-amber"
					>
						<option value="all">All scopes</option>
						<option value="repo">Repository</option>
						<option value="shared">Shared</option>
					</select>
					<select
						aria-label="Filter memories by type"
						value={type}
						onChange={(event) => updateFilter({ memory_type: event.target.value })}
						className="border border-iron bg-charcoal px-3 py-2 font-mono text-xs text-foreground outline-none focus:border-amber"
					>
						<option value="">All types</option>
						{MEMORY_TYPES.map((memoryType) => (
							<option key={memoryType} value={memoryType}>
								{memoryTypeLabel(memoryType)}
							</option>
						))}
					</select>
				</div>
			</div>

			<div className="border border-iron bg-charcoal">
				{isLoading && !data ? (
					<div
						role="status"
						className="flex items-center justify-center gap-2 py-16 font-mono text-xs text-slate-text"
					>
						<Loader2 className="h-4 w-4 animate-spin" aria-hidden />
						Loading memories…
					</div>
				) : error ? (
					<div role="alert" className="px-5 py-12 text-center font-mono text-xs text-red-400">
						Could not load memories: {error.message}
					</div>
				) : data?.memories.length ? (
					data.memories.map((memory) => <MemoryCard key={memory.id} memory={memory} />)
				) : (
					<div className="px-5 py-16 text-center">
						<Database className="mx-auto mb-3 h-7 w-7 text-slate-text" aria-hidden />
						<p className="font-mono text-xs text-slate-text">
							{query || type || scope !== "all"
								? "// No memories match these filters."
								: "// No memories have been stored yet."}
						</p>
					</div>
				)}
				<PaginationBar
					page={page}
					totalPages={totalPages}
					total={total}
					pageSize={PAGE_SIZE}
					hasNext={hasNext}
					hasPrev={hasPrev}
					onNext={() => updateParams({ memory_page: String(page + 2) })}
					onPrev={() => updateParams({ memory_page: page <= 1 ? "" : String(page) })}
				/>
			</div>
		</section>
	);
}
