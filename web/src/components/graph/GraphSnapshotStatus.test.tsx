import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { GraphSnapshot } from "@/lib/queries/architecture";
import GraphSnapshotStatus from "./GraphSnapshotStatus";

const complete: GraphSnapshot = {
	generation_id: 9,
	commit_sha: "abc123",
	status: "published",
	tree_truncated: false,
	expected_files: 12,
	visited_files: 12,
	failed_files: 0,
	skipped_files: 4,
	complete: true,
	started_at: "2026-01-01T00:00:00Z",
	published_at: "2026-01-01T00:01:00Z",
};

describe("GraphSnapshotStatus", () => {
	it("shows complete state without a warning alert", () => {
		render(<GraphSnapshotStatus snapshot={complete} />);
		expect(screen.getByText("Graph current · 12 files")).toBeTruthy();
		expect(screen.queryByRole("alert")).toBeNull();
	});

	it("announces building progress while preserving the published topology", () => {
		render(
			<GraphSnapshotStatus
				snapshot={{
					...complete,
					status: "building",
					complete: false,
					visited_files: 5,
					failed_files: 1,
				}}
			/>,
		);
		const status = screen.getByRole("status");
		expect(status.textContent).toContain("Building latest graph");
		expect(status.textContent).toContain("5 of 12 files processed");
		expect(status.textContent).toContain("1 failed and will be retried");
		expect(status.textContent).toContain("Showing the last complete topology");
	});

	it("announces failed and truncated generations as alerts with counts", () => {
		const { rerender } = render(
			<GraphSnapshotStatus
				snapshot={{
					...complete,
					status: "failed",
					complete: false,
					visited_files: 7,
					failed_files: 2,
				}}
			/>,
		);
		expect(screen.getByRole("alert").textContent).toContain("Graph update failed");
		expect(screen.getByRole("alert").textContent).toContain("7 of 12 files processed; 2 failed");

		rerender(
			<GraphSnapshotStatus
				snapshot={{
					...complete,
					status: "failed",
					complete: false,
					tree_truncated: true,
					visited_files: 0,
				}}
			/>,
		);
		expect(screen.getByRole("alert").textContent).toContain("repository tree was truncated");
	});

	it("shows explicit no-generation and incomplete states", () => {
		const empty: GraphSnapshot = {
			generation_id: 0,
			commit_sha: "",
			status: "",
			tree_truncated: false,
			expected_files: 0,
			visited_files: 0,
			failed_files: 0,
			skipped_files: 0,
			complete: false,
			started_at: "0001-01-01T00:00:00Z",
		};
		const { rerender } = render(<GraphSnapshotStatus snapshot={empty} />);
		expect(screen.getByRole("status").textContent).toContain("Graph has not been indexed yet");

		rerender(
			<GraphSnapshotStatus snapshot={{ ...complete, status: "superseded", complete: false }} />,
		);
		expect(screen.getByRole("status").textContent).toContain("Latest graph snapshot is incomplete");
		expect(screen.getByRole("status").textContent).toContain("12 of 12 files processed");
	});
});
