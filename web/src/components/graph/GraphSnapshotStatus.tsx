import type { GraphSnapshot } from "@/lib/queries/architecture";

type Props = { snapshot: GraphSnapshot };

function progress(snapshot: GraphSnapshot): string {
	const failed = snapshot.failed_files > 0 ? `; ${snapshot.failed_files} failed` : "";
	return `${snapshot.visited_files} of ${snapshot.expected_files} files processed${failed}`;
}

export default function GraphSnapshotStatus({ snapshot }: Props) {
	if (snapshot.complete) {
		return (
			<p className="px-5 py-1 text-[10px] font-mono text-slate-600 border-b border-iron/60">
				<span aria-hidden="true" className="mr-1 text-emerald-700">
					●
				</span>
				Graph current · {snapshot.expected_files} {snapshot.expected_files === 1 ? "file" : "files"}
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

	if (snapshot.tree_truncated) {
		return (
			<p
				role="alert"
				className="px-5 py-2 text-[10px] font-mono text-red-300 border-b border-red-900/50 bg-red-950/20"
			>
				Graph update failed: the repository tree was truncated. {progress(snapshot)}. Showing the
				last complete topology when available.
			</p>
		);
	}

	if (snapshot.status === "failed") {
		return (
			<p
				role="alert"
				className="px-5 py-2 text-[10px] font-mono text-red-300 border-b border-red-900/50 bg-red-950/20"
			>
				Graph update failed: {progress(snapshot)}. Showing the last complete topology when
				available.
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
				Building latest graph: {snapshot.visited_files} of {snapshot.expected_files} files processed
				{snapshot.failed_files > 0 ? `; ${snapshot.failed_files} failed${retry}` : ""}. Showing the
				last complete topology when available.
			</p>
		);
	}

	return (
		<p
			role="status"
			aria-live="polite"
			className="px-5 py-2 text-[10px] font-mono text-amber-300 border-b border-amber-900/50 bg-amber-950/20"
		>
			Latest graph snapshot is incomplete: {progress(snapshot)}. Showing the last complete topology
			when available.
		</p>
	);
}
