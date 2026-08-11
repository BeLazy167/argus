import type { GraphSnapshot } from "@/lib/queries/architecture";

type Props = { snapshot: GraphSnapshot };

function progress(snapshot: GraphSnapshot): string {
	const failed = snapshot.failed_files > 0 ? `; ${snapshot.failed_files} failed` : "";
	return `${snapshot.visited_files} of ${snapshot.expected_files} files processed${failed}`;
}

function shortCommit(commit: string): string {
	return commit.slice(0, 7);
}

function publishedTopology(snapshot: GraphSnapshot): string {
	if (!snapshot.published_commit_sha) {
		return "No complete topology is available yet.";
	}
	return `Showing topology published from ${shortCommit(snapshot.published_commit_sha)}.`;
}

export default function GraphSnapshotStatus({ snapshot }: Props) {
	if (snapshot.current) {
		return (
			<p className="px-5 py-1 text-[10px] font-mono text-slate-600 border-b border-iron/60">
				<span aria-hidden="true" className="mr-1 text-emerald-700">
					●
				</span>
				Graph current at {shortCommit(snapshot.published_commit_sha)} · {snapshot.expected_files}{" "}
				{snapshot.expected_files === 1 ? "file" : "files"}
			</p>
		);
	}

	if (snapshot.tree_truncated) {
		return (
			<p
				role="alert"
				className="px-5 py-2 text-[10px] font-mono text-red-300 border-b border-red-900/50 bg-red-950/20"
			>
				Graph update failed: the repository tree was truncated. {progress(snapshot)}.{" "}
				{publishedTopology(snapshot)}
			</p>
		);
	}

	if (snapshot.status === "failed") {
		return (
			<p
				role="alert"
				className="px-5 py-2 text-[10px] font-mono text-red-300 border-b border-red-900/50 bg-red-950/20"
			>
				Graph update failed: {progress(snapshot)}. {publishedTopology(snapshot)}
			</p>
		);
	}

	if (snapshot.status === "building") {
		const retry = snapshot.failed_files > 0 ? " and will be retried" : "";
		return (
			<p
				role="status"
				aria-live="polite"
				className="px-5 py-2 text-[10px] font-mono text-amber-300 border-b border-amber-900/50 bg-amber-950/20"
			>
				Building graph at {shortCommit(snapshot.commit_sha)}: {snapshot.visited_files} of{" "}
				{snapshot.expected_files} files processed
				{snapshot.failed_files > 0 ? `; ${snapshot.failed_files} failed${retry}` : ""}.{" "}
				{publishedTopology(snapshot)}
			</p>
		);
	}

	if (snapshot.refresh_requested_at || (snapshot.default_head_sha && snapshot.default_head_sha !== snapshot.published_commit_sha)) {
		const target = snapshot.default_head_sha ? ` for ${shortCommit(snapshot.default_head_sha)}` : "";
		return (
			<p
				role="status"
				aria-live="polite"
				className="px-5 py-2 text-[10px] font-mono text-amber-300 border-b border-amber-900/50 bg-amber-950/20"
			>
				Graph refresh queued{target}. {publishedTopology(snapshot)}
			</p>
		);
	}

	if (snapshot.generation_id === 0) {
		return (
			<p
				role="status"
				aria-live="polite"
				className="px-5 py-2 text-[10px] font-mono text-slate-500 border-b border-iron bg-card/20"
			>
				Graph has not been indexed yet.
			</p>
		);
	}

	if (snapshot.complete) {
		return (
			<p
				role="status"
				className="px-5 py-2 text-[10px] font-mono text-slate-500 border-b border-iron bg-card/20"
			>
				Graph published from {shortCommit(snapshot.published_commit_sha || snapshot.commit_sha)}, but
				 default-branch freshness has not been verified.
			</p>
		);
	}

	return (
		<p
			role="status"
			aria-live="polite"
			className="px-5 py-2 text-[10px] font-mono text-amber-300 border-b border-amber-900/50 bg-amber-950/20"
		>
			Latest graph snapshot is incomplete: {progress(snapshot)}. {publishedTopology(snapshot)}
		</p>
	);
}
