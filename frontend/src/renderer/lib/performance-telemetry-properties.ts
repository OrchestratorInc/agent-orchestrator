/**
 * Property allowlists for the performance events: the launch fields on
 * ao.app.loaded and ao.ui.performance_summary. Only counts, durations, buckets
 * and values from the closed lists below pass. See docs/telemetry.md,
 * "Performance". Kept free of imports so telemetry.ts can delegate here.
 */

type Properties = Record<string, unknown>;

export const PERFORMANCE_EVENTS: ReadonlySet<string> = new Set(["ao.app.loaded", "ao.ui.performance_summary"]);

export const LAUNCH_KINDS = ["cold", "warm", "update"] as const;
export const FLUSH_REASONS = ["interval", "startup"] as const;
/** Parts of the app a slow interaction can be attributed to, from the nearest data-telemetry-area. */
export const PERFORMANCE_AREAS = [
	"sidebar",
	"tabs",
	"chat",
	"composer",
	"terminal",
	"board",
	"inspector",
	"browser",
	"cmdk",
	"settings",
	"other",
] as const;
/** Long Animation Frame script invoker types, plus "other" for a frame with no script attributed. */
export const LONG_FRAME_CAUSES = [
	"event-listener",
	"user-callback",
	"resolve-promise",
	"reject-promise",
	"classic-script",
	"module-script",
	"other",
] as const;
export const OPEN_SESSIONS_BUCKETS = ["0", "1-3", "4-10", "11-30", "31+"] as const;
export const MEMORY_BUCKETS = ["lt256", "lt512", "lt1024", "lt2048", "ge2048"] as const;
export const UPTIME_BUCKETS = ["lt1", "lt8", "lt24", "lt72", "ge72"] as const;

/** Every whole-number property of ao.ui.performance_summary. */
export const PERFORMANCE_SUMMARY_COUNTS = [
	"window_seconds",
	"active_seconds",
	"switch_cached_lt150_count",
	"switch_cached_lt500_count",
	"switch_cached_lt1000_count",
	"switch_cached_ge1000_count",
	"switch_uncached_lt150_count",
	"switch_uncached_lt500_count",
	"switch_uncached_lt1000_count",
	"switch_uncached_ge1000_count",
	"switch_visible_p75_ms",
	"switch_dropped_count",
	"interaction_count",
	"slow_interaction_count",
	"very_slow_interaction_count",
	"interaction_p98_ms",
	"long_frame_count",
	"long_frame_blocking_ms",
	"chat_open_count",
	"chat_open_p75_ms",
] as const;

const MAX_COUNT = 10_000_000;
const MAX_LAUNCH_MS = 600_000;

function wholeNumber(value: unknown, max = MAX_COUNT): number | undefined {
	return typeof value === "number" && Number.isFinite(value) && value >= 0 ? Math.min(Math.round(value), max) : undefined;
}

function oneDecimal(value: unknown, max = 10_000): number | undefined {
	return typeof value === "number" && Number.isFinite(value) && value >= 0 ? Math.min(Math.round(value * 10) / 10, max) : undefined;
}

function oneOf<T extends string>(value: unknown, allowed: readonly T[]): T | undefined {
	return typeof value === "string" && (allowed as readonly string[]).includes(value) ? (value as T) : undefined;
}

export function sanitizePerformanceProperties(event: string, properties?: Properties): Properties {
	const candidates: Properties = {};
	if (event === "ao.app.loaded") {
		candidates.launch_kind = oneOf(properties?.launch_kind, LAUNCH_KINDS);
		// A launch without its kind is a reload; its timings would not be a launch.
		if (candidates.launch_kind !== undefined) {
			candidates.to_visible_ms = wholeNumber(properties?.to_visible_ms, MAX_LAUNCH_MS);
			candidates.to_usable_ms = wholeNumber(properties?.to_usable_ms, MAX_LAUNCH_MS);
		}
	} else if (event === "ao.ui.performance_summary") {
		candidates.measurement_schema_version = properties?.measurement_schema_version === 1 ? 1 : undefined;
		candidates.flush_reason = oneOf(properties?.flush_reason, FLUSH_REASONS);
		for (const key of PERFORMANCE_SUMMARY_COUNTS) candidates[key] = wholeNumber(properties?.[key]);
		candidates.slowest_area = oneOf(properties?.slowest_area, PERFORMANCE_AREAS);
		candidates.long_frame_top_cause = oneOf(properties?.long_frame_top_cause, LONG_FRAME_CAUSES);
		candidates.open_sessions_bucket = oneOf(properties?.open_sessions_bucket, OPEN_SESSIONS_BUCKETS);
		candidates.renderer_mb_bucket = oneOf(properties?.renderer_mb_bucket, MEMORY_BUCKETS);
		candidates.app_mb_bucket = oneOf(properties?.app_mb_bucket, MEMORY_BUCKETS);
		candidates.app_cpu_avg_pct = oneDecimal(properties?.app_cpu_avg_pct);
		candidates.uptime_hours_bucket = oneOf(properties?.uptime_hours_bucket, UPTIME_BUCKETS);
	}
	const safe: Properties = {};
	for (const [key, value] of Object.entries(candidates)) if (value !== undefined) safe[key] = value;
	return safe;
}

export function openSessionsBucket(count: number): (typeof OPEN_SESSIONS_BUCKETS)[number] {
	if (count <= 0) return "0";
	if (count <= 3) return "1-3";
	if (count <= 10) return "4-10";
	if (count <= 30) return "11-30";
	return "31+";
}

export function memoryBucket(mb: number): (typeof MEMORY_BUCKETS)[number] {
	if (mb < 256) return "lt256";
	if (mb < 512) return "lt512";
	if (mb < 1024) return "lt1024";
	if (mb < 2048) return "lt2048";
	return "ge2048";
}

export function uptimeBucket(hours: number): (typeof UPTIME_BUCKETS)[number] {
	if (hours < 1) return "lt1";
	if (hours < 8) return "lt8";
	if (hours < 24) return "lt24";
	if (hours < 72) return "lt72";
	return "ge72";
}
