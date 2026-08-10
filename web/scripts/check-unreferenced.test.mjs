import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { test } from "node:test";

import { collectOrphans } from "./check-unreferenced.mjs";

/**
 * Writes a throwaway src/ tree and returns its absolute path.
 *
 * @param {Record<string, string>} files Paths relative to src/, mapped to text.
 * @returns {string} Absolute path of the generated src/ directory.
 */
function fixture(files) {
	const srcRoot = join(mkdtempSync(join(tmpdir(), "unref-")), "src");
	for (const [rel, text] of Object.entries(files)) {
		const full = join(srcRoot, rel);
		mkdirSync(dirname(full), { recursive: true });
		writeFileSync(full, text);
	}
	return srcRoot;
}

const cases = [
	{
		name: "route files and what they import are reachable",
		files: {
			"app/page.tsx": 'import { live } from "@/lib/live";\nexport default () => live;\n',
			"lib/live.ts": "export const live = 1;\n",
		},
		want: [],
	},
	{
		name: "a module nobody imports is an orphan",
		files: {
			"app/page.tsx": "export default () => null;\n",
			"lib/dead.ts": "export const dead = 1;\n",
		},
		want: ["lib/dead.ts"],
	},
	{
		name: "proxy.ts is an entry point",
		files: {
			"proxy.ts": 'import { auth } from "@/lib/auth";\nexport default auth;\n',
			"lib/auth.ts": "export const auth = 1;\n",
		},
		want: [],
	},
	{
		// A route-convention filename inside a private folder is not a route:
		// Next.js opts `_`-prefixed folders out of routing. Seeding the walk
		// from one hides the dead folder and everything it imports -- the very
		// _components shape this check exists to catch (#232, #240).
		name: "route filenames inside a private folder do not seed the walk",
		files: {
			"app/stats/page.tsx": "export default () => null;\n",
			"app/stats/_components/loading.tsx":
				'import { helper } from "./helper";\nexport default () => helper;\n',
			"app/stats/_components/helper.ts": "export const helper = 1;\n",
		},
		want: ["app/stats/_components/helper.ts", "app/stats/_components/loading.tsx"],
	},
	{
		name: "a private folder nested under a live segment is still not routed",
		files: {
			"app/(dashboard)/layout.tsx": "export default () => null;\n",
			"app/(dashboard)/_old/reviews/page.tsx": "export default () => null;\n",
		},
		want: ["app/(dashboard)/_old/reviews/page.tsx"],
	},
	{
		// Commenting out the last import is how a directory dies. The specifier
		// left behind in the comment used to keep the dead file "reachable".
		name: "a commented-out import does not keep a file alive",
		files: {
			"app/page.tsx":
				'// import { dead } from "@/lib/dead";\nexport default () => null;\n',
			"lib/dead.ts": "export const dead = 1;\n",
		},
		want: ["lib/dead.ts"],
	},
	{
		name: "a block-commented import does not keep a file alive",
		files: {
			"app/page.tsx":
				'/*\nimport { dead } from "@/lib/dead";\n*/\nexport default () => null;\n',
			"lib/dead.ts": "export const dead = 1;\n",
		},
		want: ["lib/dead.ts"],
	},
	{
		// stripComments anchors to the start of a line, and that anchor is the
		// entire defence against over-eager stripping -- the check does not
		// parse string literals. Drop the anchor and the `//` in this URL eats
		// the import that follows it on the same line, reporting a live module
		// as dead.
		name: "a URL earlier on the line does not swallow the import after it",
		files: {
			"app/page.tsx":
				'const url = "https://argus.reviews"; import { live } from "@/lib/live";\nexport default () => [url, live];\n',
			"lib/live.ts": "export const live = 1;\n",
		},
		want: [],
	},
	{
		name: "dynamic and require specifiers count as references",
		files: {
			"app/page.tsx":
				'const lazy = () => import("@/lib/lazy");\nconst cjs = require("@/lib/cjs");\nexport default () => [lazy, cjs];\n',
			"lib/lazy.ts": "export const lazy = 1;\n",
			"lib/cjs.ts": "export const cjs = 1;\n",
		},
		want: [],
	},
];

for (const c of cases) {
	test(c.name, () => {
		const { orphans } = collectOrphans(fixture(c.files));
		assert.deepEqual(orphans, c.want);
	});
}
