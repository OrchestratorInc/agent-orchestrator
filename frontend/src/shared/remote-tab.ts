// A Browser panel tab can show the agent's own browser: a remote session's
// host Chromium, streamed by its viewer page. The viewer reports the remote
// page it shows through console messages with this prefix, and AO shows and
// drives that page as if it were the tab's own (title, icon, address bar,
// Back/Forward), so the agent's tabs look like any other tab.
export const REMOTE_TAB_REPORT = "__ao_remote_tab__";

export type RemoteTabReport = {
	/** The agent browser tab this viewer shows. */
	tabId: string;
	/** All of the agent browser's tabs: the panel keeps one tab for each. */
	tabs: { id: string; title: string; url: string }[];
	/** A tab the agent just opened from this one, which the panel switches to. */
	follow?: string;
	/** This tab's page closed in the agent browser; next is its opener, if open. */
	closed?: { next: string };
	/** The viewer streams as the panel's tab in front (main tells it which). */
	shown: boolean;
	url: string;
	title: string;
	/** The page's icon as an image data URL, "" when none (see iconDataURL). */
	favicon: string;
	loading: boolean;
	back: boolean;
	fwd: boolean;
};

const text = (value: unknown, max: number): string => (typeof value === "string" ? value.slice(0, max) : "");

// Only web addresses reach the address bar from a report.
const webURL = (value: unknown): string => {
	const url = text(value, 8192);
	return /^https?:\/\//i.test(url) ? url : "";
};

// The tab icon comes inline, as a base64 image data URL, and is shown as is.
// AO never fetches an icon address a remote page names: the fetch would leave
// from the person's machine, on their network, with their cookies. Anything
// else is dropped.
const MAX_ICON_LENGTH = 90_000;
const ICON_DATA_URL =
	/^data:image\/(?:png|x-icon|vnd\.microsoft\.icon|gif|jpeg|webp|svg\+xml);base64,(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{4}|[A-Za-z0-9+/]{3}=|[A-Za-z0-9+/]{2}==)$/;
const iconDataURL = (value: unknown): string =>
	typeof value === "string" && value.length <= MAX_ICON_LENGTH && ICON_DATA_URL.test(value) ? value : "";

export function parseRemoteTabReport(message: string): RemoteTabReport | null {
	if (!message.startsWith(REMOTE_TAB_REPORT) || message.length > 512_000) return null;
	let raw: unknown;
	try {
		raw = JSON.parse(message.slice(REMOTE_TAB_REPORT.length));
	} catch {
		return null;
	}
	if (!raw || typeof raw !== "object") return null;
	const r = raw as Record<string, unknown>;
	const tabId = text(r.tabId, 64);
	if (!tabId) return null;
	const tabs = (Array.isArray(r.tabs) ? r.tabs : []).slice(0, 100).flatMap((value: unknown) => {
		const tab = value as Record<string, unknown> | null;
		const id = text(tab?.id, 64);
		return id ? [{ id, title: text(tab?.title, 1024), url: webURL(tab?.url) }] : [];
	});
	const follow = text(r.follow, 64);
	const closed = r.closed && typeof r.closed === "object" ? { next: text((r.closed as Record<string, unknown>).next, 64) } : undefined;
	return {
		tabId,
		tabs,
		url: webURL(r.url),
		title: text(r.title, 1024),
		favicon: iconDataURL(r.favicon),
		loading: r.loading === true,
		back: r.back === true,
		fwd: r.fwd === true,
		shown: r.shown === true,
		...(follow ? { follow } : {}),
		...(closed ? { closed } : {}),
	};
}
