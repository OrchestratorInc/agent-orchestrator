import assert from "node:assert/strict";
import { test } from "node:test";
import { mkdtemp, mkdir, writeFile, rm } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { compareRecords } from "./compare-results.mjs";

function samples(open = [100, 110, 120], overrides = {}) {
	return open.map((duration, repeat) => ({
		recordVersion: 2, workload: "largeTurn", repeat,
		commit: "a".repeat(40), source: { head: "a".repeat(40), dirty: false, sourceSha256: "a".repeat(64) },
		browser: "148", node: "24", platform: "darwin", arch: "arm64", cpuModel: "fixture", cpus: 8,
		totalMemoryBytes: 8 * 1024 ** 3, nodeHeapLimitBytes: 1536 * 1024 ** 2, playwrightWorkers: 1,
		viewport: { width: 1440, height: 1000 }, browserEnvironment: { visibility: "visible", reducedMotion: false },
		result: { fixture: { fixtureChecksum: "b".repeat(64), compactFlags: { apiCompactOption: null } },
			measurementScope: "render-to-visible", lastMessageVisibleMs: duration,
			longTasks: { maxMs: 60, totalMs: 60, count: 1 }, reactActualDurationTotalMs: 30, jsHeapUsedBytes: 1024 ** 2 },
		...structuredClone(overrides),
	}));
}

test("compares medians, includes all repeats and reports a clear sampled improvement", () => {
	const result = compareRecords(samples(), samples([70, 80, 500]));
	assert.equal(result.verdict, "HELPED IN THESE SAMPLES");
	assert.equal(result.rows[0].before, 110);
	assert.equal(result.rows[0].after, 80);
	assert.deepEqual(result.rows[0].afterRange, [70, 500]);
});

test("supporting metric regressions prevent a helped verdict", () => {
	const after = samples([50, 60, 70]);
	after.forEach(r => r.result.longTasks.maxMs = 100);
	const result = compareRecords(samples(), after);
	assert.equal(result.verdict, "REGRESSION OBSERVED");
	assert.equal(result.rows[1].regressed, true);
});

test("small differences are not called improvement and zero baselines are handled", () => {
	assert.equal(compareRecords(samples(), samples([99, 109, 119])).verdict, "NO CLEAR IMPROVEMENT");
	const before = samples(), after = samples([50, 60, 70]);
	before.forEach(r => r.result.longTasks.count = 0);
	assert.equal(compareRecords(before, after).verdict, "REGRESSION OBSERVED");
});

test("single runs and missing metrics are inconclusive, never a pass", () => {
	assert.equal(compareRecords(samples([100]), samples([50])).verdict, "INCONCLUSIVE");
	const after = samples([50, 60, 70]); after[0].result.jsHeapUsedBytes = null;
	assert.equal(compareRecords(samples(), after).verdict, "INCONCLUSIVE");
	assert.equal(compareRecords(samples(), after).rows[5].missing, true);
});

test("different fixture, compact flags, scope, browser or viewport cannot be compared", () => {
	for (const mutate of [
		r => r.result.fixture.fixtureChecksum = "c".repeat(64),
		r => r.result.fixture.compactFlags.apiCompactOption = true,
		r => r.result.measurementScope = "navigation",
		r => r.browser = "different",
		r => r.viewport.width = 800,
	]) {
		const after = samples(); after.forEach(mutate);
		assert.throws(() => compareRecords(samples(), after), /mismatch/);
	}
});

test("different revisions between sides are allowed, mixed revisions within a side are rejected", () => {
	const after = samples([50, 60, 70], { commit: "c".repeat(40), source: { head: "c".repeat(40), sourceSha256: "c".repeat(64), dirty: false } });
	assert.equal(compareRecords(samples(), after).verdict, "HELPED IN THESE SAMPLES");
	after[0].source.sourceSha256 = "d".repeat(64);
	assert.throws(() => compareRecords(samples(), after), /Mixed source/);
});

test("dirty, diagnostic, incomplete, duplicate and empty samples are rejected", () => {
	for (const mutate of [r => r.source.dirty = true, r => r.diagnosticOverhead = true,
		r => r.result.lastMessageVisibleMs = null, r => delete r.browser]) {
		const after = samples(); after.forEach(mutate);
		assert.throws(() => compareRecords(samples(), after));
	}
	const before = samples(); before.push(before[0]);
	assert.throws(() => compareRecords(before, samples()), /Duplicate/);
	assert.throws(() => compareRecords([], samples()), /Both directories/);
});

test("CLI reads saved samples, skips disclosure checks, prints changes and rejects malformed input", async () => {
	const root = await mkdtemp(path.join(os.tmpdir(), "ao-compare-"));
	try {
		const before = path.join(root, "before"), after = path.join(root, "after");
		await Promise.all([mkdir(before), mkdir(after)]);
		for (const [dir, records] of [[before, samples()], [after, samples([50, 60, 70])]]) {
			await Promise.all(records.map((r, i) => writeFile(path.join(dir, `${i}.json`), JSON.stringify(r))));
			await writeFile(path.join(dir, "disclosure.json"), JSON.stringify({ workload: "largeTurnDisclosure" }));
		}
		const run = () => spawnSync(process.execPath, [fileURLToPath(new URL("./compare-results.mjs", import.meta.url)), before, after], { encoding: "utf8" });
		let result = run(); assert.equal(result.status, 0, result.stderr);
		assert.match(result.stdout, /HELPED IN THESE SAMPLES/);
		assert.match(result.stdout, /Last message visible/);
		assert.match(result.stdout, /-45.5%/);
		assert.match(result.stdout, /Ignored 2 disclosure/);
		await writeFile(path.join(after, "bad.json"), "{");
		result = run(); assert.equal(result.status, 1); assert.match(result.stderr, /Cannot compare/);
	} finally { await rm(root, { recursive: true, force: true }); }
});
