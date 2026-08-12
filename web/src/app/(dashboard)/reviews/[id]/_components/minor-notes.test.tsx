import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { MinorNotes } from "./minor-notes";

const note = {
  id: "note-1",
  review_id: "review-1",
  attempt_generation: 2,
  file_path: "src/parser.tsx",
  line: 41,
  severity: "suggestion",
  title: "Keep <script>alert('unsafe')</script> as text",
  created_at: "2026-01-01T00:00:00Z",
};

describe("MinorNotes", () => {
  it("renders structured note fields as safe text", () => {
    const { container } = render(<MinorNotes notes={[note]} />);

    expect(screen.getByRole("heading", { name: "Minor Notes" })).toBeTruthy();
    expect(screen.getByText("src/parser.tsx:41")).toBeTruthy();
    expect(screen.getByText("suggestion")).toBeTruthy();
    expect(screen.getByText(note.title)).toBeTruthy();
    expect(container.querySelector("script")).toBeNull();
  });

  it("omits the section when there are no notes", () => {
    render(<MinorNotes notes={[]} />);
    expect(screen.queryByRole("heading", { name: "Minor Notes" })).toBeNull();
  });
});
