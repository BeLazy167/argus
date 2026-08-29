import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { POST } from "./route";

const SECRET = "route-test-secret";

beforeEach(() => {
	process.env.MERMAID_VALIDATOR_SECRET = SECRET;
});

afterEach(() => {
	delete process.env.MERMAID_VALIDATOR_SECRET;
});

function request(source: string, secret = SECRET): Request {
	return new Request("http://localhost/api/internal/mermaid/validate", {
		method: "POST",
		headers: { "content-type": "application/json", "x-argus-mermaid-secret": secret },
		body: JSON.stringify({ source }),
	});
}

describe("Mermaid validator route", () => {
	it("rejects callers without the shared secret", async () => {
		const response = await POST(request("flowchart TD\n A --> B", "wrong-secret-value"));
		expect(response.status).toBe(401);
	});

	it("rejects equal-character-length secrets with different UTF-8 byte lengths", async () => {
		process.env.MERMAID_VALIDATOR_SECRET = "sécret1234";
		const response = await POST(request("flowchart TD\n A --> B", "secret1234"));
		expect(response.status).toBe(401);
	})

	it("returns the deployed parser version for valid syntax", async () => {
		const response = await POST(request("flowchart TD\n A --> B"));
		expect(response.status).toBe(200);
		await expect(response.json()).resolves.toMatchObject({ valid: true, version: "11.13.0" });
	});

	it("rejects invalid Mermaid syntax", async () => {
		const response = await POST(request("flowchart TD\n A -->"));
		expect(response.status).toBe(422);
		await expect(response.json()).resolves.toMatchObject({ valid: false, error: "invalid_syntax" });
	});
});
