import { timingSafeEqual } from "node:crypto";
import { NextResponse } from "next/server";
import { MAX_MERMAID_SOURCE_BYTES, validateMermaidSource } from "@/lib/server/mermaid-validator";

export const runtime = "nodejs";

export async function POST(request: Request) {
	const expectedSecret = process.env.MERMAID_VALIDATOR_SECRET ?? "";
	const suppliedSecret = request.headers.get("x-argus-mermaid-secret") ?? "";
	if (!secretsMatch(expectedSecret, suppliedSecret)) {
		return NextResponse.json(
			{ valid: false, error: "unauthorized" },
			{ status: expectedSecret ? 401 : 503 },
		);
	}
	const contentLength = Number(request.headers.get("content-length") ?? "0");
	if (contentLength > MAX_MERMAID_SOURCE_BYTES + 1_024) {
		return NextResponse.json({ valid: false, error: "request_too_large" }, { status: 413 });
	}
	let body: unknown;
	try {
		body = await request.json();
	} catch {
		return NextResponse.json({ valid: false, error: "invalid_json" }, { status: 400 });
	}
	const source = (body as { source?: unknown })?.source;
	if (typeof source !== "string") {
		return NextResponse.json({ valid: false, error: "source_required" }, { status: 400 });
	}
	const result = await validateMermaidSource(source);
	return NextResponse.json(result, { status: result.valid ? 200 : 422 });
}

function secretsMatch(expected: string, supplied: string): boolean {
	if (!expected) return false;
	const expectedBytes = Buffer.from(expected);
	const suppliedBytes = Buffer.from(supplied);
	if (expectedBytes.length !== suppliedBytes.length) return false;
	return timingSafeEqual(expectedBytes, suppliedBytes);
}
