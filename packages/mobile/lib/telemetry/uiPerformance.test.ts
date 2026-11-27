import { describe, expect, it } from "vitest";
import {
	emptyPerf,
	hasPerf,
	mergePerf,
	perfProperties,
	perfPropertyNames,
	recordRequest,
	recordResume,
	recordResumeFresh,
	recordResumeStale,
	recordSessionOpen,
	recordStall,
	restorePerf,
} from "./uiPerformance";

describe("perfProperties", () => {
	it("buckets session opens and reports their p75", () => {
		const perf = emptyPerf();
		for (const ms of [200, 400, 800, 2000, 5000]) recordSessionOpen(perf, ms);
		expect(perfProperties(perf)).toMatchObject({
			session_open_count: 5,
			session_open_lt500_count: 2,
			session_open_lt1000_count: 1,
			session_open_lt3000_count: 1,
			session_open_ge3000_count: 1,
			session_open_p75_ms: 2000,
		});
	});

	it("splits request latency by connection kind and counts failures apart", () => {
		const perf = emptyPerf();
		recordRequest(perf, 120, "lan", true);
		recordRequest(perf, 180, "lan", true);
		recordRequest(perf, 1500, "tailscale", true);
		recordRequest(perf, 400, "tunnel", false);
		recordRequest(perf, null, "tailscale", false);
		const props = perfProperties(perf);
		expect(props).toMatchObject({
			request_count: 4,
			request_failed_count: 2,
			request_lt300_count: 2,
			request_lt1000_count: 1,
			request_lt3000_count: 1,
			request_lan_count: 2,
			request_lan_p75_ms: 180,
			request_tailscale_count: 1,
			request_tailscale_p75_ms: 1500,
			request_tunnel_count: 1,
		});
		expect(props).not.toHaveProperty("request_relay_count");
	});

	it("counts stalls past 100 ms, long ones past 500 ms", () => {
		const perf = emptyPerf();
		for (const ms of [40, 150, 700]) recordStall(perf, ms);
		expect(perfProperties(perf)).toMatchObject({ stall_count: 2, stall_long_count: 1, stall_total_ms: 850, stall_max_ms: 700 });
	});

	it("times returns to fresh data and counts stale ones", () => {
		const perf = emptyPerf();
		for (let i = 0; i < 3; i += 1) recordResume(perf);
		recordResumeFresh(perf, 800);
		recordResumeFresh(perf, 45_000);
		recordResumeStale(perf);
		expect(perfProperties(perf)).toMatchObject({
			resume_count: 3,
			resume_fresh_lt1000_count: 1,
			resume_fresh_ge3000_count: 1,
			resume_stale_count: 2,
		});
	});

	// The last 100 samples set the percentiles; buckets still add up to every request.
	it("keeps counting past the kept samples", () => {
		const perf = emptyPerf();
		for (let i = 0; i < 250; i += 1) recordRequest(perf, i < 150 ? 100 : 2000, "lan", true);
		const props = perfProperties(perf);
		expect(props.request_count).toBe(250);
		expect(props.request_p75_ms).toBe(2000);
		// Buckets count every request, not only the kept samples.
		expect(props).toMatchObject({ request_lt300_count: 150, request_lt3000_count: 100 });
	});

	it("leaves out what was not measured", () => {
		expect(perfProperties(emptyPerf())).toEqual({ measurement_schema_version: 1 });
		expect(hasPerf(emptyPerf())).toBe(false);
	});

	it("only produces names on the allowlist", () => {
		const perf = emptyPerf();
		for (const ms of [100, 600, 1200, 4000]) {
			recordSessionOpen(perf, ms);
			recordResumeFresh(perf, ms);
			for (const transport of ["lan", "tailscale", "tunnel", "relay", "unknown"] as const) recordRequest(perf, ms, transport, false);
		}
		recordStall(perf, 900);
		recordResume(perf);
		const names = new Set(perfPropertyNames());
		expect(Object.keys(perfProperties(perf)).filter((name) => !names.has(name))).toEqual([]);
	});
});

describe("mergePerf and restorePerf", () => {
	it("adds two windows together", () => {
		const a = emptyPerf();
		const b = emptyPerf();
		recordRequest(a, 100, "lan", true);
		recordRequest(b, 300, "lan", true);
		recordStall(a, 200);
		recordStall(b, 900);
		const props = perfProperties(mergePerf(a, b));
		expect(props).toMatchObject({ request_count: 2, request_lan_count: 2, stall_count: 2, stall_max_ms: 900 });
	});

	it("survives a save and restore, and ignores anything else", () => {
		const perf = emptyPerf();
		recordRequest(perf, 250, "tailscale", true);
		expect(perfProperties(restorePerf(JSON.parse(JSON.stringify(perf))))).toEqual(perfProperties(perf));
		expect(restorePerf(undefined)).toEqual(emptyPerf());
		expect(restorePerf({ sessionOpens: "x" })).toEqual(emptyPerf());
	});
});
