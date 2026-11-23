// Where the app reopens after restarting into an OTA update. `reloadAsync()`
// starts the JS from scratch at the root route, so without this a user who
// tapped Restart inside a chat would land back on the board. The page they were
// on is saved just before the reload and reopened once, right after it.
//
// Pure rules here; storage in updateResumeStore.ts, navigation in
// UpdateRouteKeeper.tsx — the same split as onboarding.ts / onboardingStore.ts.

export const RESUME_ROUTE_KEY = "ao.updateResumeRoute";

/**
 * A saved page older than this is ignored. The reload takes seconds; anything
 * older is left over from a reload that never happened (the app was killed
 * mid-restart), and reopening it on some later launch would be a surprise.
 */
export const RESUME_MAX_AGE_MS = 2 * 60 * 1000;

export type RouteKind =
	/** A page worth reopening, as a full href. */
	| { kind: "page"; href: string }
	/** The board itself: nothing to reopen. */
	| { kind: "root" }
	/**
	 * A sheet, the onboarding flow or pairing: not a place to come
	 * back to on its own (a picker sheet returns its result to the page that
	 * opened it), so the page underneath is what gets reopened.
	 */
	| { kind: "transient" };

// Every route the root layout presents as a sheet — the `sheets/` group plus
// the ones declared beside it — and the setup screens. Pages beside them, such
// as the Files pages next to `files/[id]/review`, stay resumable.
const TRANSIENT = [
	/^\/sheets(\/|$)/,
	/^\/conversation-actions$/,
	/^\/files\/[^/]+\/review$/,
	/^\/cloud\/new-project$/,
	/^\/onboarding(\/|$)/,
	/^\/pair(\/|$)/,
	/^\/connect$/,
];
const DYNAMIC_SEGMENT = /^\[(?:\.\.\.)?([^\]]+)\]$/;

/**
 * Classify the current route. `pathname` is expo-router's resolved path
 * (`/session/abc`), `segments` its file segments (`["session", "[id]"]`) and
 * `params` its global params — which repeat the dynamic segments (`id`) beside
 * the real query (`hostId`). Only the query is appended, so the href is exactly
 * the one a deep link would use.
 */
export function classifyRoute(
	pathname: string,
	segments: readonly string[],
	params: Readonly<Record<string, string | string[] | undefined>>,
): RouteKind {
	if (pathname === "/" || pathname === "") return { kind: "root" };
	if (TRANSIENT.some((pattern) => pattern.test(pathname))) return { kind: "transient" };
	const inPath = new Set<string>();
	for (const segment of segments) {
		const match = DYNAMIC_SEGMENT.exec(segment);
		if (match) inPath.add(match[1]);
	}
	const query: string[] = [];
	for (const [key, value] of Object.entries(params)) {
		if (inPath.has(key) || value === undefined) continue;
		for (const one of Array.isArray(value) ? value : [value]) {
			query.push(`${encodeURIComponent(key)}=${encodeURIComponent(one)}`);
		}
	}
	return { kind: "page", href: query.length > 0 ? `${pathname}?${query.join("&")}` : pathname };
}

/** The route as one comparable string: a page's full href, otherwise its path. */
export function routeHref(
	pathname: string,
	segments: readonly string[],
	params: Readonly<Record<string, string | string[] | undefined>>,
): string {
	const route = classifyRoute(pathname, segments, params);
	return route.kind === "page" ? route.href : pathname || "/";
}

/**
 * How to reopen the saved page from where the reload landed (`landing`, the
 * route showing when navigation first became ready). Routes are compared as
 * full hrefs (`routeHref`): the same session path on another machine
 * (`?hostId=`) is a different page.
 *
 * Usually the board: push, so Back leads home. But expo-router reopens the last
 * deep link after a reload — a notification tapped long before — and that stale
 * page must not win over the page the user was on a second ago, so it is
 * replaced. If the user (or anything else) has already moved away from the
 * landing page, the restore is dropped: replacing a page they chose would be
 * worse than not restoring at all.
 */
export function restoreAction(current: string, landing: string, saved: string): "none" | "drop" | "push" | "replace" {
	if (current !== landing) return "drop";
	if (current === saved) return "none";
	return current === "/" || current === "" ? "push" : "replace";
}

export function encodeResumeRoute(href: string, now: number): string {
	return JSON.stringify({ href, at: now });
}

/** The page to reopen, or null when there is none, it is stale, or the record is unreadable. */
export function decodeResumeRoute(raw: string | null, now: number, maxAge = RESUME_MAX_AGE_MS): string | null {
	if (!raw) return null;
	try {
		const parsed = JSON.parse(raw) as { href?: unknown; at?: unknown };
		if (typeof parsed.href !== "string" || !parsed.href.startsWith("/")) return null;
		if (typeof parsed.at !== "number" || now - parsed.at > maxAge || parsed.at > now) return null;
		return parsed.href;
	} catch {
		return null;
	}
}
