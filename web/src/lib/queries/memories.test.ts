import { describe, expect, it } from "vitest";
import { buildMemoriesPath } from "./memories";

describe("memories API contract", () => {
  it("always sends installation scope and encodes optional filters", () => {
    expect(
      buildMemoriesPath({
        installationId: 42,
        repoId: 7,
        scope: "repo",
        type: "file synthesis",
        query: "auth & billing",
        limit: 25,
        offset: 50,
      }),
    ).toBe(
      "/api/v1/memories?installation_id=42&repo_id=7&scope=repo&type=file+synthesis&q=auth+%26+billing&limit=25&offset=50",
    );
  });

  it("omits empty filters rather than widening them to sentinel values", () => {
    expect(
      buildMemoriesPath({
        installationId: 42,
        scope: "all",
        type: "",
        query: "  ",
        limit: 25,
        offset: 0,
      }),
    ).toBe("/api/v1/memories?installation_id=42&scope=all&limit=25&offset=0");
  });
});
