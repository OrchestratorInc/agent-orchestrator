/**
 * Remote "What's new" feed: a small static JSON file the team publishes with
 * the docs site (frontend/src/docs/public/whats-new/feed.json, served from
 * docs.orchestrator.inc). The app fetches it anonymously, so this module only
 * accepts a closed shape and drops anything it does not recognize.
 */

export const WHATS_NEW_FEED_URL = "https://docs.orchestrator.inc/whats-new/feed.json";

/** Images and videos load only from the feed's own host, so showing an item contacts no third party. */
export const WHATS_NEW_MEDIA_ORIGIN = "https://docs.orchestrator.inc";

/** An item stays live this long after publishing unless it sets its own expiry. */
export const WHATS_NEW_DEFAULT_LIFETIME_MS = 30 * 24 * 60 * 60 * 1000;

const MAX_ITEMS = 20;
const ITEM_ID = /^[a-z0-9][a-z0-9._-]{0,63}$/;
const VERSION = /^\d{1,4}(\.\d{1,4}){0,2}$/;

export type WhatsNewKind = "announcement" | "nudge";

/** `url` opens in the browser; `action: "update"` opens the app's update settings. */
export type WhatsNewCta = { label: string; url: string } | { label: string; action: "update" };

export interface WhatsNewItem {
	id: string;
	/** "nudge" targets a version range, e.g. "Update to fix X" for versions below N. */
	kind: WhatsNewKind;
	title: string;
	body: string;
	/** ISO date or date-time. Items are hidden before this moment. */
	publishedAt: string;
	/** ISO date or date-time; defaults to 30 days after publishedAt. */
	expiresAt?: string;
	imageUrl?: string;
	videoUrl?: string;
	cta?: WhatsNewCta;
	/** Shown on app versions >= minVersion (inclusive). */
	minVersion?: string;
	/** Shown on app versions < maxVersion (exclusive). */
	maxVersion?: string;
}

export interface WhatsNewFeed {
	/** The remote kill switch: false hides every item. */
	enabled: boolean;
	items: WhatsNewItem[];
}

/** An eligible item as the renderer receives it. */
export interface WhatsNewEntry extends WhatsNewItem {
	seen: boolean;
}

export function isWhatsNewItemId(value: unknown): value is string {
	return typeof value === "string" && ITEM_ID.test(value);
}

function text(value: unknown, max: number): string | undefined {
	if (typeof value !== "string") return undefined;
	const trimmed = value.trim();
	return trimmed && trimmed.length <= max ? trimmed : undefined;
}

function date(value: unknown): string | undefined {
	return typeof value === "string" && !Number.isNaN(Date.parse(value)) ? value : undefined;
}

function httpsUrl(value: unknown, origin?: string): string | undefined {
	if (typeof value !== "string") return undefined;
	try {
		const url = new URL(value);
		if (url.protocol !== "https:" || url.username || url.password) return undefined;
		if (origin && url.origin !== origin) return undefined;
		return url.toString();
	} catch {
		return undefined;
	}
}

function cta(value: unknown): WhatsNewCta | undefined {
	if (!value || typeof value !== "object") return undefined;
	const raw = value as Record<string, unknown>;
	const label = text(raw.label, 40);
	if (!label) return undefined;
	if (raw.action === "update") return { label, action: "update" };
	const url = httpsUrl(raw.url);
	return url ? { label, url } : undefined;
}

function item(value: unknown): WhatsNewItem | null {
	if (!value || typeof value !== "object") return null;
	const raw = value as Record<string, unknown>;
	const id = isWhatsNewItemId(raw.id) ? raw.id : undefined;
	const kind = raw.kind === "nudge" || raw.kind === "announcement" ? raw.kind : raw.kind === undefined ? "announcement" : undefined;
	const title = text(raw.title, 120);
	const body = text(raw.body, 600);
	const publishedAt = date(raw.publishedAt);
	if (!id || !kind || !title || !body || !publishedAt) return null;
	const parsed: WhatsNewItem = { id, kind, title, body, publishedAt };
	const expiresAt = date(raw.expiresAt);
	if (expiresAt) parsed.expiresAt = expiresAt;
	const imageUrl = httpsUrl(raw.imageUrl, WHATS_NEW_MEDIA_ORIGIN);
	if (imageUrl) parsed.imageUrl = imageUrl;
	const videoUrl = httpsUrl(raw.videoUrl, WHATS_NEW_MEDIA_ORIGIN);
	if (videoUrl) parsed.videoUrl = videoUrl;
	const action = cta(raw.cta);
	if (action) parsed.cta = action;
	for (const key of ["minVersion", "maxVersion"] as const) {
		const version = raw[key];
		if (typeof version === "string" && VERSION.test(version)) parsed[key] = version;
	}
	return parsed;
}

/** Parses an untrusted feed document, or returns null when it is not a version-1 feed. */
export function parseWhatsNewFeed(value: unknown): WhatsNewFeed | null {
	if (!value || typeof value !== "object") return null;
	const raw = value as Record<string, unknown>;
	if (raw.version !== 1) return null;
	const seen = new Set<string>();
	const items: WhatsNewItem[] = [];
	for (const candidate of Array.isArray(raw.items) ? raw.items : []) {
		const parsed = item(candidate);
		if (!parsed || seen.has(parsed.id)) continue;
		seen.add(parsed.id);
		items.push(parsed);
		if (items.length === MAX_ITEMS) break;
	}
	return { enabled: raw.enabled !== false, items };
}
