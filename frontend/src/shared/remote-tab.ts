export const REMOTE_TAB_REPORT = "__ao_remote_tab__";

export type RemoteTabReport = {
	tabId: string;
	tabs: { id: string; title: string; url: string }[];
	follow?: string;
	closed?: { next: string };
	shown: boolean;
	url: string;
	title: string;
	favicon: string;
	loading: boolean;
	back: boolean;
	fwd: boolean;
};

const text = (value: unknown, max: number): string => (typeof value === "string" ? value.slice(0, max) : "");

const webURL = (value: unknown): string => {
	const url = text(value, 8192);
	return /^https?:\/\//i.test(url) ? url : "";
};

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
