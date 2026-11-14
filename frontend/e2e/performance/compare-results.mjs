import { readFile, readdir } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const metrics = [
	["Last message visible", "ms", r => r.lastMessageVisibleMs],
	["Longest long task", "ms", r => r.longTasks?.maxMs],
	["Total long-task time", "ms", r => r.longTasks?.totalMs],
	["Long-task count", "", r => r.longTasks?.count],
	["React render work", "ms", r => r.reactActualDurationTotalMs],
	["JS heap", "MiB", r => typeof r.jsHeapUsedBytes === "number" ? r.jsHeapUsedBytes / 1024 ** 2 : null],
];
const median = values => {
	const sorted = [...values].sort((a, b) => a - b);
	const middle = Math.floor(sorted.length / 2);
	return sorted.length % 2 ? sorted[middle] : (sorted[middle - 1] + sorted[middle]) / 2;
};
const valid = value => typeof value === "number" && Number.isFinite(value) && value >= 0;
const key = value => JSON.stringify(value);

function identity(record) {
	if (record.recordVersion !== 2 || record.workload !== "largeTurn" || !record.result?.fixture
		|| record.diagnosticOverhead || record.source?.dirty !== false
		|| !/^[a-f0-9]{40}$/.test(record.commit ?? "") || record.commit !== record.source?.head || !/^[a-f0-9]{64}$/.test(record.source?.sourceSha256 ?? "")) {
		throw new Error("Expected clean, completed version-2 largeTurn records; diagnostic captures are not comparable.");
	}
	const required = [record.browser, record.node, record.platform, record.arch, record.cpuModel,
		record.cpus, record.totalMemoryBytes, record.nodeHeapLimitBytes, record.playwrightWorkers,
		record.viewport, record.browserEnvironment, record.result.measurementScope];
	if (required.some(value => value === undefined || value === null)
		|| !/^[a-f0-9]{64}$/.test(record.result.fixture.fixtureChecksum ?? "")
		|| !valid(record.result.lastMessageVisibleMs)) throw new Error("Missing capture identity or completed visibility measurement.");
	return key({ fixture: record.result.fixture, environment: required });
}

/** Descriptive comparison only: no hardware-independent or statistical performance assertion. */
export function compareRecords(before, after) {
	if (!before.length || !after.length) throw new Error("Both directories need largeTurn measurement records.");
	const identities = [...before, ...after].map(identity);
	if (new Set(identities).size !== 1) throw new Error("Fixture, measurement scope or environment mismatch. Use the same workload/browser/host settings.");
	for (const records of [before, after]) {
		if (new Set(records.map(r => key([r.commit, r.source.sourceSha256]))).size !== 1) throw new Error("Mixed source revisions within one side.");
		if (new Set(records.map(key)).size !== records.length) throw new Error("Duplicate samples found; do not count copied results as repeats.");
	}
	const rows = metrics.map(([name, unit, get]) => {
		const left = before.map(r => get(r.result));
		const right = after.map(r => get(r.result));
		if (![...left, ...right].every(valid)) return { name, unit, missing: true };
		const baseline = median(left), candidate = median(right);
		const percent = baseline === 0 ? (candidate === 0 ? 0 : null) : (candidate - baseline) / baseline * 100;
		return { name, unit, before: baseline, after: candidate, percent,
			regressed: baseline === 0 ? candidate > 0 : percent > 5,
			beforeRange: [Math.min(...left), Math.max(...left)], afterRange: [Math.min(...right), Math.max(...right)] };
	});
	const insufficient = before.length < 3 || after.length < 3 || rows.some(row => row.missing);
	const regression = rows.some(row => row.regressed);
	const faster = rows[0].percent !== null && rows[0].percent < -5;
	const verdict = insufficient ? "INCONCLUSIVE" : regression ? "REGRESSION OBSERVED" : faster ? "HELPED IN THESE SAMPLES" : "NO CLEAR IMPROVEMENT";
	return { verdict, rows, beforeCount: before.length, afterCount: after.length,
		beforeHead: before[0].commit, afterHead: after[0].commit,
		reason: insufficient ? "Need at least 3 samples per side and every metric present." : "Rule: visibility median improves by more than 5%, with no supporting metric worsening by more than 5% (zero-to-positive also counts as worse)." };
}

async function load(directory) {
	const records = [];
	let disclosures = 0;
	for (const entry of (await readdir(directory, { withFileTypes: true })).sort((a, b) => a.name.localeCompare(b.name))) {
		if (!entry.isFile() || !entry.name.endsWith(".json")) continue;
		const record = JSON.parse(await readFile(path.join(directory, entry.name), "utf8"));
		if (record.workload === "largeTurnDisclosure") { disclosures++; continue; }
		if (record.workload !== "largeTurn") throw new Error("Directory contains an unrelated JSON file; use directories containing saved measurement samples only.");
		records.push(record);
	}
	return { records, disclosures };
}

export async function main(args) {
	if (args.length !== 2) throw new Error("Usage: node e2e/performance/compare-results.mjs BEFORE_DIR AFTER_DIR");
	const [before, after] = await Promise.all(args.map(load));
	const result = compareRecords(before.records, after.records);
	console.log(`${result.verdict}\nBefore: ${result.beforeHead} (${result.beforeCount} samples)\nAfter:  ${result.afterHead} (${result.afterCount} samples)`);
	console.log("\nMetric                    Before median   After median   Change (lower is better)");
	for (const row of result.rows) {
		if (row.missing) { console.log(`${row.name.padEnd(26)}unavailable — comparison incomplete`); continue; }
		const number = value => `${value.toFixed(2)}${row.unit ? " " + row.unit : ""}`;
		const change = row.percent === null ? "from zero" : `${row.percent > 0 ? "+" : ""}${row.percent.toFixed(1)}%`;
		console.log(`${row.name.padEnd(26)}${number(row.before).padEnd(16)}${number(row.after).padEnd(15)}${change}${row.regressed ? "  WORSE" : ""}`);
	}
	const open = result.rows[0];
	console.log(`\nVisibility sample range: before ${open.beforeRange.map(v => v.toFixed(2)).join("–")} ms; after ${open.afterRange.map(v => v.toFixed(2)).join("–")} ms.`);
	console.log(result.reason);
	console.log(`Ignored ${before.disclosures + after.disclosures} disclosure-check records (not timing samples).`);
	console.log("Descriptive medians only, not statistical proof. Match launch flags, warm-up/cache policy and host load manually. React render work is not commit wall time; heap is not GC-normalized. Synthetic results do not qualify real-chat targets.");
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
	main(process.argv.slice(2)).catch(error => { console.error(`Cannot compare: ${error.message}`); process.exitCode = 1; });
}
