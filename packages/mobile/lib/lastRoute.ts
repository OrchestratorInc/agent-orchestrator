// Which screen was open, so the app can go back to it after the phone closed
// it in the background. Phones close background apps to free memory (cheaper
// ones often), and the app used to come back on the board every time: a chat,
// a file or a preview the reader was in the middle of was simply gone.
//
// Only reading screens are reopened (a chat, files, an artifact, a preview, a
// shell, New task, a review), never a sheet, pairing or onboarding; and only
// soon after the app was left, when coming back to it is what the person
// expects. A launch from a link or a notification goes where that points.

/** How long after leaving the app it still reopens where it was. */
export const RESTORE_WITHIN_MS = 30 * 60 * 1000;

export const LAST_ROUTE_KEY = "ao.lastRoute.v1";

export type LastRoute = { href: string | null; leftAt: number | null };

const RESTORABLE = /^\/(session|files|artifact|artifacts|preview|shell|review|reviewer)\/[^/]|^\/spawn(\?|$)/;

/**
 * The screen's address: its path, with the parameters the path does not
 * already carry as a query. Which those are comes from the route's segments
 * (`["files", "[sessionId]", "file"]`): a parameter named by a dynamic segment
 * is in the path; any other stays, even one whose value happens to match a
 * segment (`path=file` on `/files/web-1/file`).
 */
export function routeHref(pathname: string, params: Record<string, string | string[] | undefined>, segments: readonly string[] = []): string {
	const inPath = new Set(segments.flatMap((segment) => {
		const name = /^\[(?:\.\.\.)?([^\]]+)\]$/.exec(segment)?.[1];
		return name ? [name] : [];
	}));
	const query = new URLSearchParams();
	for (const [key, value] of Object.entries(params)) {
		const one = Array.isArray(value) ? value[0] : value;
		if (one === undefined || one === "" || inPath.has(key)) continue;
		query.set(key, one);
	}
	const text = query.toString();
	return text ? `${pathname}?${text}` : pathname;
}

export function restorableHref(href: string | null | undefined): href is string {
	return typeof href === "string" && RESTORABLE.test(href);
}

/**
 * Where to reopen at launch, or null for the board: the screen open when the
 * app was left, if that was recently. The caller decides whether something
 * else (a link, a notification tap, the person) launched it somewhere first.
 */
export function routeToRestore(saved: LastRoute | null, now: number): string | null {
	if (!saved || !restorableHref(saved.href) || saved.leftAt === null) return null;
	if (now - saved.leftAt > RESTORE_WITHIN_MS || now < saved.leftAt) return null;
	return saved.href;
}

export function parseLastRoute(raw: string | null): LastRoute | null {
	if (!raw) return null;
	try {
		const value = JSON.parse(raw) as Partial<LastRoute>;
		return {
			href: typeof value.href === "string" ? value.href : null,
			leftAt: typeof value.leftAt === "number" ? value.leftAt : null,
		};
	} catch {
		return null;
	}
}
