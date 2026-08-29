import { describe, expect, it } from "vitest";
import { validateMermaidSource } from "./mermaid-validator";

describe("validateMermaidSource", () => {
	it("uses Mermaid 11 to accept valid syntax", async () => {
		await expect(validateMermaidSource("flowchart TD\n  A --> B")).resolves.toMatchObject({
			valid: true,
		});
	});

	it("rejects syntax that a bracket check accepts", async () => {
		await expect(validateMermaidSource("flowchart TD\n  A -->")).resolves.toMatchObject({
			valid: false,
		});
	});

	it("rejects oversized input before parsing", async () => {
		await expect(
			validateMermaidSource(`flowchart TD\n${"A".repeat(20_001)}`),
		).resolves.toMatchObject({ valid: false });
	});
});
