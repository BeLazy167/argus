import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import type { LearnedMemory, LearnedMemoryCount } from "@/lib/types";
import { LearnedMemories } from "./learned-memory";

/**
 * The panel and the posted PR comment describe the same review, so they must
 * name its memory types with the same words. They used to hold separate tables:
 * the backend had explicit singular/plural pairs, the panel had its own labels
 * plus `count === 1 ? "" : "s"`. For a review that wrote two PR summaries the
 * comment said "2 PR summaries" and the page said "2 PR summarys".
 *
 * The fix removed the second table. The server sends `label`, already singular
 * or plural, from `store.LearnedMemoryLabel`, and this component renders it.
 * These tests fail if the panel ever derives a noun again.
 */

function count(type: string, n: number, label: string): LearnedMemoryCount {
  return { type, count: n, label };
}

function memory(type: string, label: string, excerpt: string): LearnedMemory {
  return {
    type,
    label,
    container_tag: "acme/web",
    excerpt,
    written_at: new Date().toISOString(),
  };
}

afterEach(cleanup);

describe("LearnedMemories", () => {
  it("renders the server's plural noun, not a naive +s", () => {
    render(
      <LearnedMemories
        memories={[]}
        counts={[count("pr_summary", 2, "PR summaries")]}
        status="completed"
      />,
    );

    const panel = screen.getByLabelText("What Argus learned from this review");
    expect(panel.textContent).toContain("2 PR summaries");
    // The exact string a locally-derived label produces. Its absence is the
    // whole assertion: the panel must not be pluralizing anything itself.
    expect(panel.textContent).not.toContain("summarys");
  });

  it("renders the server's singular noun verbatim, casing included", () => {
    render(
      <LearnedMemories
        memories={[]}
        counts={[count("synthesis", 1, "file memory")]}
        status="completed"
      />,
    );

    const panel = screen.getByLabelText("What Argus learned from this review");
    // Verbatim: the comment says "file memory", so the chip must too. The old
    // local table capitalized it to "File memory", which is the same drift in
    // a quieter form.
    expect(panel.textContent).toContain("1 file memory");
    expect(panel.textContent).not.toContain("File memory");
  });

  it("labels each entry with the server's singular noun", () => {
    render(
      <LearnedMemories
        memories={[
          memory("synthesis", "file memory", "what this file does"),
          memory("pr_summary", "PR summary", "what this PR does"),
        ]}
        counts={[count("synthesis", 1, "file memory"), count("pr_summary", 1, "PR summary")]}
        status="completed"
      />,
    );

    const panel = screen.getByLabelText("What Argus learned from this review");
    // "File memorys" was the old naive rendering of a synthesis entry.
    expect(panel.textContent).not.toContain("memorys");
    expect(screen.getByText("file memory")).toBeDefined();
    expect(screen.getByText("PR summary")).toBeDefined();
  });

  it("reports an unmapped type by its raw name instead of dropping it", () => {
    render(
      <LearnedMemories
        memories={[]}
        counts={[count("future_type", 2, "future_type")]}
        status="completed"
      />,
    );

    const panel = screen.getByLabelText("What Argus learned from this review");
    expect(panel.textContent).toContain("2 future_type");
    expect(panel.textContent).not.toContain("future_types");
  });

  it("says plainly when a finished review wrote nothing", () => {
    render(<LearnedMemories memories={[]} counts={[]} status="completed" />);

    const panel = screen.getByLabelText("What Argus learned from this review");
    expect(panel.textContent).toContain("wrote nothing to memory");
  });

  it("renders nothing while the review is still running", () => {
    const { container } = render(
      <LearnedMemories memories={[]} counts={[]} status="running" />,
    );
    expect(container.innerHTML).toBe("");
  });
});
