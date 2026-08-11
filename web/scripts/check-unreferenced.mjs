#!/usr/bin/env node
/**
 * Fails when a .ts/.tsx module under src/ is reachable from no entry point.
 *
 * Why this exists: `next build` and `tsc --noEmit` both compile files nobody
 * imports, so a file can rot in the tree for months while every gate stays
 * green. Twice now (#232, #240) an "extracted components" directory was left
 * behind unimported next to a page that still defines the same components
 * inline, and the copies drifted -- stats/_components/stat-cards.tsx kept a
 * Loading/Err pair with no role/aria-live long after the shadscan a11y fix
 * landed on the live stats/page.tsx copy. Editing the dead copy compiles,
 * builds, deploys, and changes nothing a user sees.
 *
 * Reachability, not a filename blocklist: colocating components under a
 * private `_components/` folder is a legitimate Next.js pattern. The defect is
 * the file nobody imports, whatever it is named.
 */

import { readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join, relative, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";

const WEB_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const SRC = join(WEB_ROOT, "src");

/**
 * Filenames Next.js loads by convention. Nothing imports these, so graph walks
 * that seed only from imports would call the entire app dead.
 */
const ROUTE_FILES = new Set([
	"page",
	"layout",
	"loading",
	"error",
	"global-error",
	"not-found",
	"template",
	"default",
	"route",
	"forbidden",
	"unauthorized",
	"sitemap",
	"robots",
	"opengraph-image",
	"twitter-image",
	"icon",
	"apple-icon",
	"manifest",
]);

/**
 * Loaded by the framework from a fixed path, outside app/. `proxy.ts` is the
 * Next.js 16 name for what was `middleware.ts`; omitting it reports the app's
 * entire auth layer as dead code.
 */
const ROOT_ENTRIES = [
	"proxy.ts",
	"middleware.ts",
	"instrumentation.ts",
	"instrumentation-client.ts",
];

const MODULE_EXTS = [".ts", ".tsx"];

/** Every .ts/.tsx file under dir, as absolute paths. */
function walk(dir) {
	const out = [];
	for (const entry of readdirSync(dir)) {
		const full = join(dir, entry);
		if (statSync(full).isDirectory()) {
			out.push(...walk(full));
			continue;
		}
		if (MODULE_EXTS.some((ext) => entry.endsWith(ext))) out.push(full);
	}
	return out;
}

/**
 * Removes comments so a commented-out import cannot keep a dead file alive.
 * Commenting out the last import of a directory is the usual way that
 * directory dies, and the specifier left behind in the comment made this check
 * report the whole dead subtree as reachable.
 *
 * Both patterns anchor to the start of a line. This check does NOT parse
 * string literals, and the anchor is the whole reason it does not have to: a
 * `//` or a slash-star in the middle of a line is far more often inside a
 * string -- "https://x", a "**\/*.ts" glob -- than a comment, and swallowing
 * a string would delete the real imports after it and report live files as
 * dead. Telling the two apart needs a tokenizer that also handles template
 * literals and regex literals, which is not worth it here.
 *
 * The cost of the anchor is a known blind spot: a commented-out import that
 * does not start its own line -- `foo(); // import x from "./x"` -- is still
 * read as a reference and keeps that file alive. The dominant case, and the
 * one that killed the directories in #232 and #240, is a whole commented-out
 * import line, which is stripped.
 *
 * @param {string} text Source text of one module.
 * @returns {string} The same text with whole-line comments blanked out.
 */
function stripComments(text) {
	return text
		.replace(/^[ \t]*\/\*[\s\S]*?\*\//gm, "")
		.replace(/^[ \t]*\/\/.*$/gm, "");
}

/**
 * Pulls every module specifier out of source text: static `from "x"`,
 * side-effect `import "x"`, dynamic `import("x")`, and `require("x")`.
 * Deliberately regex-based rather than AST-based -- a missed specifier only
 * costs a false "unreferenced" report, which a human reads before deleting,
 * and this must run with zero dependencies in CI.
 */
function specifiersOf(text) {
	const source = stripComments(text);
	const specs = [];
	const patterns = [
		/\bfrom\s*["']([^"']+)["']/g,
		/\bimport\s*["']([^"']+)["']/g,
		/\bimport\s*\(\s*["']([^"']+)["']\s*\)/g,
		/\brequire\s*\(\s*["']([^"']+)["']\s*\)/g,
	];
	for (const re of patterns) {
		let m = re.exec(source);
		while (m !== null) {
			specs.push(m[1]);
			m = re.exec(source);
		}
	}
	return specs;
}

/** Mirrors tsconfig `paths` (@/* -> ./src/*) and bundler extension probing. */
function resolveSpec(spec, fromFile, srcRoot) {
	let base;
	if (spec.startsWith("@/")) base = join(srcRoot, spec.slice(2));
	else if (spec.startsWith(".")) base = resolve(dirname(fromFile), spec);
	else return null; // package import

	const candidates = [
		base,
		...MODULE_EXTS.map((ext) => base + ext),
		...MODULE_EXTS.map((ext) => join(base, `index${ext}`)),
	];
	for (const candidate of candidates) {
		if (!MODULE_EXTS.some((ext) => candidate.endsWith(ext))) continue;
		try {
			if (statSync(candidate).isFile()) return candidate;
		} catch {
			// candidate does not exist; try the next extension
		}
	}
	return null;
}

/**
 * True when Next.js loads this file itself, rather than something importing it.
 *
 * A route-convention filename inside a private folder is NOT one: Next.js opts
 * an `_`-prefixed folder and every subfolder out of routing entirely. Without
 * this, a dead `_components/loading.tsx` seeds the walk as a live entry and
 * drags its whole import subtree in with it -- the exact directory shape this
 * check exists to catch (#232, #240), invisible again for the sake of a
 * filename.
 *
 * @param {string} file Absolute path to a module.
 * @param {string} srcRoot Absolute path to the src/ directory.
 * @returns {boolean} Whether the framework loads the file by convention.
 */
function isEntry(file, srcRoot) {
	const rel = relative(srcRoot, file).split(sep).join("/");
	if (ROOT_ENTRIES.includes(rel)) return true;
	// A test file is an entry point of the TEST runner: vitest globs it, so
	// nothing in src/ ever imports it. Without this the guard reports every test
	// as dead and blocks it from being added at all — which is how it flagged
	// mermaid-chart.test.tsx, a test that does run, the moment one landed.
	// Its imports still count as references, so a module used only by a test is
	// deliberately NOT an orphan.
	if (/\.(test|spec)\.[jt]sx?$/.test(rel)) return true;
	if (!rel.startsWith("app/")) return false;
	const segments = rel.split("/");
	const name = segments.pop();
	if (segments.some((dir) => dir.startsWith("_"))) return false;
	return ROUTE_FILES.has(name.replace(/\.tsx?$/, ""));
}

/**
 * Walks the import graph from the framework's entry points and reports what it
 * never reaches.
 *
 * @param {string} srcRoot Absolute path to the src/ directory to analyse.
 * @returns {{orphans: string[], total: number}} Orphan paths relative to
 *   srcRoot with `/` separators, sorted, plus the module count scanned.
 */
export function collectOrphans(srcRoot) {
	const all = walk(srcRoot);
	const entries = all.filter((f) => isEntry(f, srcRoot));

	const reached = new Set(entries);
	const queue = [...entries];
	while (queue.length > 0) {
		const file = queue.pop();
		for (const spec of specifiersOf(readFileSync(file, "utf8"))) {
			const target = resolveSpec(spec, file, srcRoot);
			if (target === null || reached.has(target)) continue;
			reached.add(target);
			queue.push(target);
		}
	}

	const orphans = all
		.filter((f) => !reached.has(f))
		.map((f) => relative(srcRoot, f).split(sep).join("/"))
		.sort();
	return { orphans, total: all.length };
}

function main() {
	const { orphans, total } = collectOrphans(SRC);

	if (orphans.length > 0) {
		console.error(
			`${orphans.length} module(s) under src/ are reachable from no entry point:\n`,
		);
		for (const orphan of orphans) console.error(`  src/${orphan}`);
		console.error(
			"\nDelete them, or import them from something that ships. A file nobody" +
				"\nimports still typechecks and still builds, so no other gate will" +
				"\never tell you it is dead.\n",
		);
		process.exit(1);
	}

	console.log(`ok: all ${total} modules under src/ are reachable`);
}

// Importing this file from the test must not walk the real tree or exit.
if (process.argv[1] === fileURLToPath(import.meta.url)) main();
