// Picks the github.com web URLs worth handing to the GitHub mobile app. Pure
// and free of React Native imports so it can be unit-tested directly — the
// Linking side lives in `openGitHub.ts`, mirroring how `pushStatus.ts` pairs
// with `push.ts`.
//
// Why this exists: a private repo opened in the phone's browser shows a login
// wall or a 404, because the browser has no GitHub session. The app does. So we
// hand the app anything it can render and leave the rest to the browser.
//
// The hand-off is the https URL itself, not the app's `github://` scheme. iOS
// routes github.com links into the app as universal links (github.com's
// apple-app-site-association claims repo, pull and issue pages for
// com.github.stormbreaker.prod), and the app opens the right screen. The
// `github://repo/…` paths are undocumented and have changed across releases;
// users reported the app coming to the front for a PR link and staying on
// whatever screen it was last showing.
//
// Still conservative: only the three shapes the app has a screen for, and null
// for everything else, which means "use the in-app browser".

/** Path segments of a github.com URL, or null if it isn't one. */
function segments(url: string): string[] | null {
	// Hand-rolled rather than `new URL()`: React Native's URL polyfill is
	// incomplete, and this runs on-device.
	const m = /^https?:\/\/(?:www\.)?github\.com(\/[^?#]*)?/i.exec(url.trim());
	if (!m) return null;
	return (m[1] ?? "").split("/").filter(Boolean);
}

const isNumeric = (s: string) => /^\d+$/.test(s);

/**
 * The canonical github.com URL to hand to the GitHub app, or null when the app
 * has no screen for it and the browser should be used instead.
 *
 * Notably null for `/issues/new?body=…`: the app cannot accept a prefilled
 * issue body, so sending "Report a problem" there would silently drop the
 * diagnostics it attaches.
 */
export function githubAppLink(url: string): string | null {
	const seg = segments(url);
	if (!seg || seg.length < 2) return null;
	const [owner, repo, ...rest] = seg;
	// Reserved roots that look like an owner but aren't (/settings/…, /orgs/…).
	if (owner.startsWith("_") || RESERVED_ROOTS.has(owner.toLowerCase())) return null;
	const base = `https://github.com/${owner}/${repo}`;
	if (rest.length === 0) return base;
	if (rest.length === 2 && (rest[0] === "pull" || rest[0] === "issues") && isNumeric(rest[1])) {
		return `${base}/${rest[0]}/${rest[1]}`;
	}
	return null;
}

/**
 * Probed with `Linking.canOpenURL` to learn whether the GitHub app is installed.
 * Never opened. iOS only answers for schemes in LSApplicationQueriesSchemes.
 */
export const GITHUB_APP_SCHEME = "github://";

const RESERVED_ROOTS = new Set([
	"settings",
	"orgs",
	"organizations",
	"notifications",
	"explore",
	"marketplace",
	"pulls",
	"issues",
	"search",
	"login",
	"join",
	"about",
	"features",
	"sponsors",
	"apps",
	"topics",
	"collections",
	"trending",
	"new",
	"codespaces",
]);
