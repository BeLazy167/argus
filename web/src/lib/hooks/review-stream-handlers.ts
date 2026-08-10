import type { QueryClient } from "@tanstack/react-query";
import type { Dispatch, RefObject, SetStateAction } from "react";
import type { Review, ReviewComment } from "../types";
import type {
	LiveTokens,
	PipelineStage,
	ScoringUpdate,
	TimelineEntry,
	TriageFile,
	WSEvent,
} from "./use-review-stream";

type ReviewCache = { review: Review; comments: ReviewComment[] };

/**
 * Dependency bundle threaded to every event handler.
 *
 * Mirrors exactly the closure variables `processEvent` captured before the
 * decomposition: React setters, refs, the two local cache/timeline mutators,
 * and the query-cache coordinates. Handlers must mutate only through these so
 * the hook's observable behavior is unchanged.
 */
export type HandlerContext = {
	reviewId: string;
	queryKey: readonly unknown[];
	qc: QueryClient;
	setStage: Dispatch<SetStateAction<PipelineStage>>;
	setFailedStage: Dispatch<SetStateAction<string | undefined>>;
	setTriageResults: Dispatch<SetStateAction<TriageFile[] | null>>;
	setScoringUpdate: Dispatch<SetStateAction<ScoringUpdate | null>>;
	setLiveTokens: Dispatch<SetStateAction<LiveTokens | null>>;
	seenStagesRef: RefObject<Set<PipelineStage>>;
	terminalRef: RefObject<boolean>;
	synthesisSeenRef: RefObject<boolean>;
	patchReview: (patch: Partial<Review>) => void;
	addEntry: (entry: Omit<TimelineEntry, "id" | "timestamp">) => void;
};

type EventHandler = (ctx: HandlerContext, evt: WSEvent) => void;

/**
 * Handles `stage_changed`: advances the pipeline stage, records it as seen,
 * mirrors the status onto the cached review, and appends a stage row.
 */
function handleStageChanged(ctx: HandlerContext, evt: WSEvent): void {
	ctx.setStage(evt.data.stage as PipelineStage);
	ctx.seenStagesRef.current.add(evt.data.stage as PipelineStage);
	ctx.patchReview({ status: mapStageToStatus(evt.data.stage as string) });
	ctx.addEntry({ type: "stage", message: stageMessage(evt.data.stage as string), icon: "stage" });
}

/**
 * Handles `triage_complete`: stores the classified file list and appends a
 * summary row counting classifications by action.
 */
function handleTriageComplete(ctx: HandlerContext, evt: WSEvent): void {
	ctx.setTriageResults(evt.data.files as TriageFile[]);
	ctx.addEntry({
		type: "triage",
		message: `Classified ${(evt.data.files as TriageFile[]).length} files`,
		detail: summarizeTriage(evt.data.files as TriageFile[]),
		icon: "stage",
	});
}

/**
 * Handles `file_review_started`: appends a row naming the file under review,
 * with specialist/action joined into the detail line when present.
 */
function handleFileReviewStarted(ctx: HandlerContext, evt: WSEvent): void {
	ctx.addEntry({
		type: "file",
		message: `Reviewing ${shortPath(evt.data.file_path as string)}`,
		detail: [evt.data.specialist, evt.data.action].filter(Boolean).join(" · ") || undefined,
		icon: "file",
	});
}

/**
 * Handles `comment`: appends a synthesized `ReviewComment` to the cached
 * comment list and adds a truncated timeline row for it.
 */
function handleComment(ctx: HandlerContext, evt: WSEvent): void {
	ctx.qc.setQueryData(ctx.queryKey, (old: ReviewCache | undefined) => {
		if (!old) return old;
		const comment: ReviewComment = {
			id: crypto.randomUUID(),
			review_id: ctx.reviewId,
			file_path: evt.data.file_path as string,
			end_line: evt.data.line as number,
			body: evt.data.body as string,
			severity: evt.data.severity as ReviewComment["severity"],
			category: evt.data.category as string,
			specialist: evt.data.specialist as string,
			created_at: new Date().toISOString(),
		};
		return { ...old, comments: [...old.comments, comment] };
	});
	ctx.addEntry({
		type: "comment",
		message: truncate(evt.data.body as string, 60),
		detail: `${evt.data.severity} · ${shortPath(evt.data.file_path as string)}:${evt.data.line}`,
		icon: "comment",
	});
}

/**
 * Handles `scoring_update`: stores kept/dropped counts and severity-tiered
 * cutoffs, appending a row whose detail shows the cutoffs only when present.
 */
function handleScoringUpdate(ctx: HandlerContext, evt: WSEvent): void {
	const thresholds = evt.data.thresholds as ScoringUpdate["thresholds"];
	ctx.setScoringUpdate({
		kept: evt.data.kept as number,
		dropped: evt.data.dropped as number,
		thresholds,
	});
	const detail =
		thresholds &&
		(thresholds.critical != null || thresholds.warning != null || thresholds.suggestion != null)
			? `cutoff c:${thresholds.critical ?? "—"} · w:${thresholds.warning ?? "—"} · s:${thresholds.suggestion ?? "—"}`
			: undefined;
	ctx.addEntry({
		type: "scoring",
		message: `Kept ${evt.data.kept}, dropped ${evt.data.dropped}`,
		detail,
		icon: "scoring",
	});
}

/**
 * Handles `token_update`: stores the running token total and cost. No timeline
 * row — this drives a live counter, not a discrete event.
 */
function handleTokenUpdate(ctx: HandlerContext, evt: WSEvent): void {
	ctx.setLiveTokens({
		total_tokens: evt.data.total_tokens as number,
		cost: evt.data.cost as number,
	});
}

/**
 * Handles `synthesis`: idempotently merges summary/score into the cached
 * review, then adds the "Review complete" row once, gated by `synthesisSeenRef`
 * so a reconnect replay does not duplicate it.
 */
function handleSynthesis(ctx: HandlerContext, evt: WSEvent): void {
	// patchReview is idempotent on re-delivery (it merges fields); the
	// guard only dedupes the visible timeline row.
	ctx.patchReview({
		summary: evt.data.summary as string,
		score: evt.data.score as number,
	});
	if (ctx.synthesisSeenRef.current) return;
	ctx.addEntry({
		type: "done",
		message: `Review complete — score ${evt.data.score}/10`,
		icon: "done",
	});
	ctx.synthesisSeenRef.current = true;
}

/**
 * Handles `completed`: invalidates the review/reviews caches unconditionally,
 * then (once) finalizes the stage and appends the terminal "Posted" row.
 *
 * Invalidation runs BEFORE the `terminalRef` early-return so a replayed
 * terminal event still refreshes caches (e.g. replica-lag left stale data on
 * the first delivery). Only the visible row and stage mutation are gated.
 */
function handleCompleted(ctx: HandlerContext, _evt: WSEvent): void {
	ctx.qc.invalidateQueries({ queryKey: ["review", ctx.reviewId] });
	ctx.qc.invalidateQueries({ queryKey: ["reviews"] });
	if (ctx.terminalRef.current) return;
	ctx.setStage("completed");
	ctx.addEntry({ type: "done", message: "Posted to GitHub", icon: "done" });
	ctx.terminalRef.current = true;
}

/**
 * Handles `cancelled`: invalidates caches unconditionally, then (once) sets the
 * stage to cancelled and appends a row naming the stage it was cancelled at.
 *
 * Same ordering rationale as {@link handleCompleted}: invalidation before the
 * terminal guard, visible row and stage mutation after.
 */
function handleCancelled(ctx: HandlerContext, evt: WSEvent): void {
	ctx.qc.invalidateQueries({ queryKey: ["review", ctx.reviewId] });
	ctx.qc.invalidateQueries({ queryKey: ["reviews"] });
	if (ctx.terminalRef.current) return;
	ctx.setStage("cancelled");
	ctx.addEntry({ type: "stage", message: `Cancelled at ${evt.data.stage}`, icon: "error" });
	ctx.terminalRef.current = true;
}

/**
 * Handles `error`: invalidates caches unconditionally, then (once) records the
 * failing stage, sets the stage to failed, and appends an error row.
 *
 * Same ordering rationale as {@link handleCompleted}: invalidation before the
 * terminal guard, visible row and stage mutation after.
 */
function handleError(ctx: HandlerContext, evt: WSEvent): void {
	ctx.qc.invalidateQueries({ queryKey: ["review", ctx.reviewId] });
	ctx.qc.invalidateQueries({ queryKey: ["reviews"] });
	if (ctx.terminalRef.current) return;
	ctx.setFailedStage(evt.data.stage as string);
	ctx.setStage("failed");
	ctx.addEntry({
		type: "error",
		message: `Failed at ${evt.data.stage}: ${evt.data.error}`,
		icon: "error",
	});
	ctx.terminalRef.current = true;
}

/**
 * Handles `intent_extracted`: appends a brief row carrying the extracted goal,
 * truncated, when the payload supplies a string goal.
 */
function handleIntentExtracted(ctx: HandlerContext, evt: WSEvent): void {
	const goal = typeof evt.data.goal === "string" ? evt.data.goal : undefined;
	ctx.addEntry({
		type: "intent",
		message: "Intent extracted",
		detail: goal ? truncate(goal, 80) : undefined,
		icon: "brief",
	});
}

/**
 * Handles `intent_verified`: appends a brief row stating whether the change
 * delivers on intent, with an unmet-requirement count when non-zero.
 */
function handleIntentVerified(ctx: HandlerContext, evt: WSEvent): void {
	const delivers = evt.data.delivers === true;
	const unmet = typeof evt.data.unmet === "number" ? evt.data.unmet : 0;
	ctx.addEntry({
		type: "intent",
		message: delivers ? "Intent verified: delivers" : "Intent verified: does not deliver",
		detail: unmet > 0 ? `${unmet} unmet` : undefined,
		icon: "brief",
	});
}

/**
 * Handles `findings_enriched`: appends a row reporting how many findings were
 * matched against memory, when a count is supplied.
 */
function handleFindingsEnriched(ctx: HandlerContext, evt: WSEvent): void {
	const count = typeof evt.data.count === "number" ? evt.data.count : undefined;
	ctx.addEntry({
		type: "enrichment",
		message: "Findings enriched",
		detail: count != null ? `${count} matched` : undefined,
		icon: "stage",
	});
}

/**
 * Handles `brief_generated`: appends a brief row noting the generated brief's
 * character length, when supplied.
 */
function handleBriefGenerated(ctx: HandlerContext, evt: WSEvent): void {
	const length = typeof evt.data.length === "number" ? evt.data.length : undefined;
	ctx.addEntry({
		type: "brief",
		message: "Brief generated",
		detail: length != null ? `${length} chars` : undefined,
		icon: "brief",
	});
}

/** Handles `lead_brief`: appends a row marking the lead brief as drafted. */
function handleLeadBrief(ctx: HandlerContext, _evt: WSEvent): void {
	ctx.addEntry({ type: "lead", message: "Lead brief drafted", icon: "brief" });
}

/**
 * Handles `lead_broadcast`: appends a row noting how many specialists the lead
 * brief was broadcast to, when the payload is an array.
 */
function handleLeadBroadcast(ctx: HandlerContext, evt: WSEvent): void {
	const specialists = Array.isArray(evt.data.specialists) ? evt.data.specialists.length : undefined;
	ctx.addEntry({
		type: "lead",
		message: "Lead broadcast",
		detail: specialists != null ? `${specialists} specialists` : undefined,
		icon: "stage",
	});
}

/**
 * Handles `second_pass`: appends a row reporting how many files the second
 * review pass covered, when supplied.
 */
function handleSecondPass(ctx: HandlerContext, evt: WSEvent): void {
	const files = typeof evt.data.files === "number" ? evt.data.files : undefined;
	ctx.addEntry({
		type: "pass2",
		message: "Second pass",
		detail: files != null ? `${files} files` : undefined,
		icon: "stage",
	});
}

/**
 * Handles `blast_radius`: appends a validation row reporting how many symbols
 * the change is estimated to affect, when supplied.
 */
function handleBlastRadius(ctx: HandlerContext, evt: WSEvent): void {
	const affected = typeof evt.data.affected === "number" ? evt.data.affected : undefined;
	ctx.addEntry({
		type: "blast",
		message: "Blast radius analyzed",
		detail: affected != null ? `${affected} affected` : undefined,
		icon: "validation",
	});
}

/**
 * Handles `lead_cross_check`: appends a validation row reporting how many
 * findings the lead cross-check matched, when supplied.
 */
function handleLeadCrossCheck(ctx: HandlerContext, evt: WSEvent): void {
	const matched = typeof evt.data.matched === "number" ? evt.data.matched : undefined;
	ctx.addEntry({
		type: "lead",
		message: "Lead cross-check",
		detail: matched != null ? `${matched} matches` : undefined,
		icon: "validation",
	});
}

/**
 * Handles `acceptance_checked`: appends a validation row summarizing accepted
 * vs. rejected findings, when either count is present.
 */
function handleAcceptanceChecked(ctx: HandlerContext, evt: WSEvent): void {
	const accepted = typeof evt.data.accepted === "number" ? evt.data.accepted : undefined;
	const rejected = typeof evt.data.rejected === "number" ? evt.data.rejected : undefined;
	const detail =
		accepted != null || rejected != null
			? `accepted ${accepted ?? 0} · rejected ${rejected ?? 0}`
			: undefined;
	ctx.addEntry({ type: "acceptance", message: "Acceptance checked", detail, icon: "validation" });
}

/**
 * Handles `cross_pr_checked`: appends a validation row reporting how many
 * cross-PR incompatibilities were found, when supplied.
 */
function handleCrossPrChecked(ctx: HandlerContext, evt: WSEvent): void {
	const incompat =
		typeof evt.data.incompatibilities === "number" ? evt.data.incompatibilities : undefined;
	ctx.addEntry({
		type: "cross_pr",
		message: "Cross-PR checked",
		detail: incompat != null ? `${incompat} incompatibilities` : undefined,
		icon: "validation",
	});
}

/**
 * Handles `simulations_complete`: appends a simulation row summarizing how many
 * simulations passed out of the total, when a total is supplied.
 */
function handleSimulationsComplete(ctx: HandlerContext, evt: WSEvent): void {
	const total = typeof evt.data.total === "number" ? evt.data.total : undefined;
	const passed = typeof evt.data.passed === "number" ? evt.data.passed : undefined;
	const detail = total != null ? `${passed ?? 0}/${total} passed` : undefined;
	ctx.addEntry({ type: "simulation", message: "Simulations complete", detail, icon: "simulation" });
}

/**
 * Handles `scenario_simulated`: appends a simulation row identifying the
 * scenario and its verdict, joining whichever parts are present.
 */
function handleScenarioSimulated(ctx: HandlerContext, evt: WSEvent): void {
	const verdict = typeof evt.data.verdict === "string" ? evt.data.verdict : undefined;
	const id = typeof evt.data.scenario_id === "number" ? evt.data.scenario_id : undefined;
	const detail = [id != null ? `#${id}` : null, verdict].filter(Boolean).join(" · ") || undefined;
	ctx.addEntry({ type: "simulation", message: "Scenario simulated", detail, icon: "simulation" });
}

/**
 * Handles `memory_indexed`: appends a memory row naming the indexed kind, and
 * marking the row as failed when the payload reports `success === false`.
 */
function handleMemoryIndexed(ctx: HandlerContext, evt: WSEvent): void {
	const kind = typeof evt.data.kind === "string" ? evt.data.kind : undefined;
	const success = evt.data.success !== false;
	ctx.addEntry({
		type: "memory",
		message: kind ? `Memory indexed (${kind})` : "Memory indexed",
		detail: success ? undefined : "failed",
		icon: "memory",
	});
}

/**
 * Handles `posted_to_github`: appends a row marking the GitHub post succeeded
 * while post-processing continues, with inline/folded comment counts.
 */
function handlePostedToGithub(ctx: HandlerContext, evt: WSEvent): void {
	// Mid-stage signal: GitHub POST succeeded. The terminal "completed"
	// event fires later after memory indexing, backfill, and pattern
	// learning finish, so phrase this one distinctly to avoid two
	// indistinguishable "Posted to GitHub" rows on the timeline.
	const inline = typeof evt.data.inline === "number" ? evt.data.inline : undefined;
	const folded = typeof evt.data.folded === "number" ? evt.data.folded : undefined;
	const detail =
		inline != null || folded != null ? `${inline ?? 0} inline · ${folded ?? 0} folded` : undefined;
	ctx.addEntry({
		type: "done",
		message: "Posted to GitHub (post-processing continues)",
		detail,
		icon: "done",
	});
}

/**
 * Handles `reply_generated`: appends a reply row noting the generated reply's
 * character length, when supplied.
 */
function handleReplyGenerated(ctx: HandlerContext, evt: WSEvent): void {
	const length = typeof evt.data.length === "number" ? evt.data.length : undefined;
	ctx.addEntry({
		type: "reply",
		message: "Reply generated",
		detail: length != null ? `${length} chars` : undefined,
		icon: "reply",
	});
}

/**
 * Handles `memory_matched`: appends a memory row attributing a finding to a
 * Supermemory-backed pattern, with kind and source PR when present.
 */
function handleMemoryMatched(ctx: HandlerContext, evt: WSEvent): void {
	// Fired by enrichFindings when a finding matches a Supermemory-backed
	// pattern / convention / rule / similarity hit. Detail line carries
	// kind + source PR so authors can trace the attribution shown on the
	// inline comment body.
	const kind = typeof evt.data.kind === "string" ? evt.data.kind : undefined;
	const pr = typeof evt.data.pr === "number" && evt.data.pr > 0 ? evt.data.pr : undefined;
	const parts: string[] = [];
	if (kind) parts.push(kind);
	if (pr != null) parts.push(`PR #${pr}`);
	ctx.addEntry({
		type: "memory",
		message: "Memory match",
		detail: parts.length > 0 ? parts.join(" · ") : undefined,
		icon: "memory",
	});
}

/**
 * Dispatch table mapping each WebSocket event type to its handler.
 *
 * A missing key resolves to `undefined`, so {@link dispatchEvent} no-ops on
 * unknown event types — identical to the original switch's absent `default`.
 */
const handlers: Record<string, EventHandler> = {
	stage_changed: handleStageChanged,
	triage_complete: handleTriageComplete,
	file_review_started: handleFileReviewStarted,
	comment: handleComment,
	scoring_update: handleScoringUpdate,
	token_update: handleTokenUpdate,
	synthesis: handleSynthesis,
	completed: handleCompleted,
	cancelled: handleCancelled,
	error: handleError,
	intent_extracted: handleIntentExtracted,
	intent_verified: handleIntentVerified,
	findings_enriched: handleFindingsEnriched,
	brief_generated: handleBriefGenerated,
	lead_brief: handleLeadBrief,
	lead_broadcast: handleLeadBroadcast,
	second_pass: handleSecondPass,
	blast_radius: handleBlastRadius,
	lead_cross_check: handleLeadCrossCheck,
	acceptance_checked: handleAcceptanceChecked,
	cross_pr_checked: handleCrossPrChecked,
	simulations_complete: handleSimulationsComplete,
	scenario_simulated: handleScenarioSimulated,
	memory_indexed: handleMemoryIndexed,
	posted_to_github: handlePostedToGithub,
	reply_generated: handleReplyGenerated,
	memory_matched: handleMemoryMatched,
};

/**
 * Routes a single stream event to its handler, doing nothing for unrecognized
 * event types.
 *
 * Args:
 *   ctx: The dependency bundle the matched handler mutates through.
 *   evt: The decoded WebSocket event whose `type` selects the handler.
 */
export function dispatchEvent(ctx: HandlerContext, evt: WSEvent): void {
	handlers[evt.type]?.(ctx, evt);
}

function mapStageToStatus(stage: string): Review["status"] {
	if (stage === "completed") return "completed";
	if (stage === "failed") return "failed";
	if (stage === "cancelled") return "cancelled";
	return "in_progress";
}

function stageMessage(stage: string): string {
	const map: Record<string, string> = {
		triaging: "Triaging files...",
		briefing: "Building lead brief...",
		reviewing: "Starting file reviews...",
		deduping: "Deduplicating findings...",
		validating: "Validating (SAST + blast + acceptance + cross-PR)...",
		scoring: "Scoring comments...",
		pass2: "Re-reviewing hot files...",
		synthesizing: "Generating synthesis...",
		posting: "Posting to GitHub...",
	};
	return map[stage] ?? `Stage: ${stage}`;
}

function summarizeTriage(files: TriageFile[]): string {
	const counts: Record<string, number> = {};
	for (const f of files) {
		counts[f.action] = (counts[f.action] ?? 0) + 1;
	}
	return Object.entries(counts)
		.map(([k, v]) => `${v} ${k}`)
		.join(" · ");
}

function shortPath(path: string): string {
	const parts = path.split("/");
	return parts.length <= 2 ? path : parts.slice(-2).join("/");
}

function truncate(str: string, max: number): string {
	if (str.length <= max) return str;
	return str.slice(0, max - 1) + "…";
}
