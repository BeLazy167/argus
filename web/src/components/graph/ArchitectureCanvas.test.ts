import type { Node } from "@xyflow/react";
import { describe, expect, it } from "vitest";
import { mergeLayoutPositions } from "./ArchitectureCanvas";

function node(id: string, x: number): Node {
  return { id, position: { x, y: 0 }, data: {} };
}

describe("architecture canvas reconciliation", () => {
  it("uses refreshed node membership while retaining positions for surviving files", () => {
    const current = [node("removed.ts", 10), node("kept.ts", 40)];
    const refreshed = [node("kept.ts", 100), node("added.ts", 200)];

    const result = mergeLayoutPositions(refreshed, current);

    expect(result.map(({ id }) => id)).toEqual(["kept.ts", "added.ts"]);
    expect(result.find(({ id }) => id === "kept.ts")?.position.x).toBe(40);
    expect(result.find(({ id }) => id === "added.ts")?.position.x).toBe(200);
  });
});
