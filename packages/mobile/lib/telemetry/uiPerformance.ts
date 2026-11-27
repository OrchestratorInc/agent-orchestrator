// How fast the app responded, counted on the phone and sent once per usage
// window as `mobile.usage.ui_performance`: the mobile counterpart of the
// desktop's ao.usage.ui_performance (docs/telemetry.md, "UI responsiveness").
// Pure: the usage tracker (uiUsage.ts) owns the window and calls these.
//
// Only counts, durations in milliseconds and fixed connection kinds leave the
// phone. No route, session, host or address is recorded.

export const PERF_EVENT = "mobile.usage.ui_performance";
export const PERF_MEASUREMENT_SCHEMA_VERSION = 1;

/** How the phone reached the machine that answered (lib/endpoints.ts). */
export const TRANSPORTS = ["lan", "tailscale", "tunnel", "relay", "unknown"] as const;
export type Transport = (typeof TRANSPORTS)[number];

/** A main-thread stall: the JS thread was this much later than asked. */
export const STALL_MS = 100;
export const LONG_STALL_MS = 500;
/** Back in front without fresh data this long counts as a stale return. */
export const RESUME_STALE_MS = 30_000;
// The last this many samples of each duration, enough for a p75 or p95.
const SAMPLES = 100;

export const SESSION_OPEN_EDGES = [500, 1000, 3000] as const;
export const REQUEST_EDGES = [300, 1000, 3000] as const;
export const RESUME_EDGES = [1000, 3000] as const;

// `next` counts every value; `values` keeps the last SAMPLES for percentiles;
// `buckets` counts every value by edge, exactly, when the metric is bucketed.
type Samples = { values: number[]; next: number; buckets?: number[] };

export type PerfWindow = {
	sessionOpens: Samples;
	stalls: { count: number; long: number; totalMs: number; maxMs: number };
	requests: Samples & { failed: number; byTransport: Partial<Record<Transport, Samples>> };
	resumes: { count: number; stale: number; fresh: Samples };
};

function emptySamples(edges?: readonly number[]): Samples {
	return edges ? { values: [], next: 0, buckets: new Array(edges.length + 1).fill(0) } : { values: [], next: 0 };
}

export function emptyPerf(): PerfWindow {
	return {
		sessionOpens: emptySamples(SESSION_OPEN_EDGES),
		stalls: { count: 0, long: 0, totalMs: 0, maxMs: 0 },
		requests: { ...emptySamples(REQUEST_EDGES), failed: 0, byTransport: {} },
		resumes: { count: 0, stale: 0, fresh: emptySamples(RESUME_EDGES) },
	};
}

function bucketOf(ms: number, edges: readonly number[]): number {
	const index = edges.findIndex((edge) => ms < edge);
	return index < 0 ? edges.length : index;
}

// Counts every value but keeps only the last SAMPLES of them.
function add(samples: Samples, value: number, edges?: readonly number[]): void {
	const ms = Math.max(0, Math.round(value));
	if (samples.values.length < SAMPLES) samples.values.push(ms);
	else samples.values[samples.next % SAMPLES] = ms;
	samples.next += 1;
	if (edges && samples.buckets) samples.buckets[bucketOf(ms, edges)] += 1;
}

function count(samples: Samples): number {
	return samples.next;
}

function percentile(samples: Samples, p: number): number | undefined {
	if (!samples.values.length) return undefined;
	const sorted = [...samples.values].sort((a, b) => a - b);
	return sorted[Math.min(sorted.length - 1, Math.ceil((p / 100) * sorted.length) - 1)];
}

function bucketCounts(samples: Samples, prefix: string, edges: readonly number[]): Record<string, number> {
	const out: Record<string, number> = {};
	const buckets = samples.buckets ?? [];
	edges.forEach((edge, index) => {
		if (buckets[index]) out[`${prefix}_lt${edge}_count`] = buckets[index];
	});
	const rest = buckets[edges.length];
	if (rest) out[`${prefix}_ge${edges[edges.length - 1]}_count`] = rest;
	return out;
}

export function recordSessionOpen(perf: PerfWindow, ms: number): void {
	add(perf.sessionOpens, ms, SESSION_OPEN_EDGES);
}

export function recordStall(perf: PerfWindow, lateMs: number): void {
	if (lateMs < STALL_MS) return;
	perf.stalls.count += 1;
	if (lateMs >= LONG_STALL_MS) perf.stalls.long += 1;
	perf.stalls.totalMs += Math.round(lateMs);
	perf.stalls.maxMs = Math.max(perf.stalls.maxMs, Math.round(lateMs));
}

/** A daemon request: its time to an answer, or `null` when none came (timeout, offline). */
export function recordRequest(perf: PerfWindow, ms: number | null, transport: Transport, ok: boolean): void {
	if (ms === null || !ok) perf.requests.failed += 1;
	if (ms === null) return;
	add(perf.requests, ms, REQUEST_EDGES);
	const byTransport = perf.requests.byTransport[transport] ?? emptySamples();
	add(byTransport, ms);
	perf.requests.byTransport[transport] = byTransport;
}

/** The app came back to the front with a machine paired. */
export function recordResume(perf: PerfWindow): void {
	perf.resumes.count += 1;
}

/** Fresh data arrived `ms` after a return to the front; past RESUME_STALE_MS it was a stale return. */
export function recordResumeFresh(perf: PerfWindow, ms: number): void {
	if (ms >= RESUME_STALE_MS) perf.resumes.stale += 1;
	add(perf.resumes.fresh, ms, RESUME_EDGES);
}

/** A return to the front that went back to the background before any fresh data. */
export function recordResumeStale(perf: PerfWindow): void {
	perf.resumes.stale += 1;
}

export function hasPerf(perf: PerfWindow): boolean {
	return count(perf.sessionOpens) > 0 || perf.stalls.count > 0 || count(perf.requests) > 0
		|| perf.requests.failed > 0 || perf.resumes.count > 0;
}

function mergeSamples(a: Samples, b: Samples): Samples {
	const out: Samples = { values: [...a.values], next: a.next };
	for (const value of b.values) add(out, value);
	// Values dropped from b before it was merged still count.
	out.next = a.next + b.next;
	if (a.buckets || b.buckets) {
		const length = Math.max(a.buckets?.length ?? 0, b.buckets?.length ?? 0);
		out.buckets = Array.from({ length }, (_, i) => (a.buckets?.[i] ?? 0) + (b.buckets?.[i] ?? 0));
	}
	return out;
}

export function mergePerf(a: PerfWindow, b: PerfWindow): PerfWindow {
	const byTransport: Partial<Record<Transport, Samples>> = { ...a.requests.byTransport };
	for (const transport of TRANSPORTS) {
		const other = b.requests.byTransport[transport];
		if (other) byTransport[transport] = mergeSamples(byTransport[transport] ?? emptySamples(), other);
	}
	return {
		sessionOpens: mergeSamples(a.sessionOpens, b.sessionOpens),
		stalls: {
			count: a.stalls.count + b.stalls.count,
			long: a.stalls.long + b.stalls.long,
			totalMs: a.stalls.totalMs + b.stalls.totalMs,
			maxMs: Math.max(a.stalls.maxMs, b.stalls.maxMs),
		},
		requests: { ...mergeSamples(a.requests, b.requests), failed: a.requests.failed + b.requests.failed, byTransport },
		resumes: { count: a.resumes.count + b.resumes.count, stale: a.resumes.stale + b.resumes.stale, fresh: mergeSamples(a.resumes.fresh, b.resumes.fresh) },
	};
}

function isSamples(value: unknown, edges?: readonly number[]): value is Samples {
	const s = value as Samples | null;
	return !!s && Array.isArray(s.values) && s.values.every((v) => Number.isFinite(v)) && Number.isFinite(s.next)
		&& (!edges || (Array.isArray(s.buckets) && s.buckets.length === edges.length + 1 && s.buckets.every((v) => Number.isFinite(v))));
}

/** A saved window's perf part, trusted only in the shape this file writes; empty otherwise. */
export function restorePerf(value: unknown): PerfWindow {
	const p = value as PerfWindow | null;
	if (!p || typeof p !== "object" || !isSamples(p.sessionOpens, SESSION_OPEN_EDGES) || !isSamples(p.requests, REQUEST_EDGES) || !isSamples(p.resumes?.fresh, RESUME_EDGES)
		|| !p.stalls || ![p.stalls.count, p.stalls.long, p.stalls.totalMs, p.stalls.maxMs, p.requests.failed, p.resumes.count, p.resumes.stale].every(Number.isFinite)
		|| typeof p.requests.byTransport !== "object" || p.requests.byTransport === null) {
		return emptyPerf();
	}
	const byTransport: Partial<Record<Transport, Samples>> = {};
	for (const transport of TRANSPORTS) {
		const samples = p.requests.byTransport[transport];
		if (isSamples(samples)) byTransport[transport] = samples;
	}
	return { ...p, requests: { ...p.requests, byTransport } };
}

/** The event's own properties. Zeros and unmeasured percentiles are left out. */
export function perfProperties(perf: PerfWindow): Record<string, number> {
	const props: Record<string, number> = { measurement_schema_version: PERF_MEASUREMENT_SCHEMA_VERSION };
	const set = (key: string, value: number | undefined) => {
		if (value !== undefined && value > 0) props[key] = value;
	};
	set("session_open_count", count(perf.sessionOpens));
	Object.assign(props, bucketCounts(perf.sessionOpens, "session_open", SESSION_OPEN_EDGES));
	set("session_open_p75_ms", percentile(perf.sessionOpens, 75));
	set("stall_count", perf.stalls.count);
	set("stall_long_count", perf.stalls.long);
	set("stall_total_ms", perf.stalls.totalMs);
	set("stall_max_ms", perf.stalls.maxMs);
	set("request_count", count(perf.requests));
	set("request_failed_count", perf.requests.failed);
	Object.assign(props, bucketCounts(perf.requests, "request", REQUEST_EDGES));
	set("request_p75_ms", percentile(perf.requests, 75));
	set("request_p95_ms", percentile(perf.requests, 95));
	for (const transport of TRANSPORTS) {
		const samples = perf.requests.byTransport[transport];
		if (!samples) continue;
		set(`request_${transport}_count`, count(samples));
		set(`request_${transport}_p75_ms`, percentile(samples, 75));
	}
	set("resume_count", perf.resumes.count);
	set("resume_stale_count", perf.resumes.stale);
	Object.assign(props, bucketCounts(perf.resumes.fresh, "resume_fresh", RESUME_EDGES));
	set("resume_fresh_p75_ms", percentile(perf.resumes.fresh, 75));
	return props;
}

/** Every property name perfProperties can produce, for the allowlist. */
export function perfPropertyNames(): string[] {
	const names = [
		"measurement_schema_version",
		"session_open_count", "session_open_p75_ms",
		"stall_count", "stall_long_count", "stall_total_ms", "stall_max_ms",
		"request_count", "request_failed_count", "request_p75_ms", "request_p95_ms",
		"resume_count", "resume_stale_count", "resume_fresh_p75_ms",
	];
	const buckets = (prefix: string, edges: readonly number[]) => [
		...edges.map((edge) => `${prefix}_lt${edge}_count`),
		`${prefix}_ge${edges[edges.length - 1]}_count`,
	];
	names.push(...buckets("session_open", SESSION_OPEN_EDGES), ...buckets("request", REQUEST_EDGES), ...buckets("resume_fresh", RESUME_EDGES));
	for (const transport of TRANSPORTS) names.push(`request_${transport}_count`, `request_${transport}_p75_ms`);
	return names;
}
