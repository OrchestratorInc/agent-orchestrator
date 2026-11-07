// Node 24 type stripping; no frontend dependencies, app, daemon or private data.
import assert from "node:assert/strict";
import { afterEach, beforeEach, test } from "node:test";
import { measureChatOpen } from "./chat-open-measurement.ts";
import { largeTurnFixture } from "./large-turn-fixture.ts";
import { fixtureIdentity } from "./fixture-identity.ts";

const globals = ["requestAnimationFrame", "cancelAnimationFrame", "innerWidth", "innerHeight", "PerformanceObserver"];
let saved;
const rect = { width: 100, height: 100, top: 10, bottom: 110, left: 10, right: 110 };
const node = { getBoundingClientRect: () => rect, closest: () => ({ getBoundingClientRect: () => rect }), checkVisibility: () => true };
beforeEach(() => {
	saved = globals.map((key) => [key, Object.getOwnPropertyDescriptor(globalThis, key)]);
	globalThis.requestAnimationFrame = (callback) => setImmediate(() => callback(performance.now()));
	globalThis.cancelAnimationFrame = clearImmediate;
	globalThis.innerWidth = 1000;
	globalThis.innerHeight = 800;
	globalThis.PerformanceObserver = undefined;
});
afterEach(() => {
	for (const [key, descriptor] of saved) {
		if (descriptor) Object.defineProperty(globalThis, key, descriptor);
		else delete globalThis[key];
	}
});

test("generated workload identity is reproducible and records compact flags", async () => {
	const snapshot = largeTurnFixture();
	const identity = await fixtureIdentity(snapshot);
	assert.deepEqual(identity, await fixtureIdentity(largeTurnFixture()));
	assert.equal(identity.turnCount, 1);
	assert.equal(identity.toolRowCount, 1850);
	assert.equal(identity.itemCount, 1926);
	assert.equal(identity.fixtureChecksum.length, 64);
	assert.equal(identity.compactFlags.apiCompactOption, null);
	assert.equal((await fixtureIdentity(snapshot, true)).compactFlags.apiCompactOption, true);
	assert.equal(identity.compactFlags.detailOmittedRows, 0);
	snapshot.items[1].detailOmitted = true;
	const changed = await fixtureIdentity(snapshot);
	assert.notEqual(changed.fixtureChecksum, identity.fixtureChecksum);
	assert.equal(changed.compactFlags.detailOmittedRows, 1);
});

test("uncaptured stages and unsupported metrics are null, not zero", async () => {
	const result = await measureChatOpen().finish(() => node);
	assert.equal(result.longTasks, null);
	assert.equal(result.reactActualDurationTotalMs, null);
	assert.equal(result.jsHeapUsedBytes, null);
	assert.equal(result.documentFirstPaintMs, null);
	assert.equal(result.reactCommitPhaseWallMs, null);
	assert.deepEqual(Object.values(result.stages), [null, null, null, null, null]);
	assert.ok(result.firstVisibleFrameMs <= result.firstPaintOpportunityMs);
});

test("stage recorder preserves values/errors and records each span once", async () => {
	const capture = measureChatOpen();
	assert.equal(capture.measureSync("conversion", () => 42), 42);
	assert.equal(await capture.measureAsync("fetch", async () => 7), 7);
	assert.throws(() => capture.measureSync("grouping", () => { throw new Error("sentinel"); }), /sentinel/);
	await assert.rejects(capture.measureAsync("json", async () => { throw new Error("sentinel"); }), /sentinel/);
	const end = capture.startStage("renderSubmission");
	end();
	end();
	const result = await capture.finish(() => node);
	for (const spans of Object.values(result.stages)) assert.equal(spans.length, 1);
	assert.equal(result.stages.grouping[0].failed, true);
	assert.equal(result.stages.json[0].failed, true);
	assert.equal(result.stages.fetch[0].failed, false);
});

test("initial long task overlapping measurement start is retained exactly once", async () => {
	globalThis.PerformanceObserver = class {
		static supportedEntryTypes = ["longtask"];
		entries = [{ startTime: performance.now() - 1, duration: 1_000_000 }];
		observe() {}
		disconnect() {}
		takeRecords() { return this.entries.splice(0); }
	};
	const result = await measureChatOpen().finish(() => node);
	assert.equal(result.longTasks.count, 1);
	assert.equal(result.longTasks.totalMs, 1_000_000);
	assert.ok(result.longTasks.entries[0].startMs < 0);
});

test("optional trace marks identify stage, commit and visibility clocks", async () => {
	const capture = measureChatOpen({ traceMarks: true });
	capture.measureSync("conversion", () => 42);
	const now = performance.now();
	capture.onRender("test-only-profiler", "mount", 1, 2, now, now);
	const result = await capture.finish(() => node);
	const marks = performance.getEntriesByType("mark").filter((entry) => entry.name.startsWith(result.traceMarkPrefix));
	assert.ok(marks.some((entry) => entry.name.endsWith(":conversion:start")));
	assert.ok(marks.some((entry) => entry.name.endsWith(":conversion:end")));
	assert.ok(marks.some((entry) => entry.name.endsWith(":react-commit")));
	assert.ok(marks.some((entry) => entry.name.endsWith(":two-frame-visible")));
	assert.equal(result.reactCommits[0].actualDurationMs, 1);
	for (const mark of marks) performance.clearMarks(mark.name);
});

test("missing message and stalled frames reject rather than report a partial pass", async () => {
	await assert.rejects(measureChatOpen().finish(() => null, 10), /measurement incomplete/);
	globalThis.requestAnimationFrame = () => 0;
	await assert.rejects(measureChatOpen().finish(() => node, 10), /measurement incomplete/);
});
