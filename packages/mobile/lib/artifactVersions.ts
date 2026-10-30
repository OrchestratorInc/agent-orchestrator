/**
 * An artifact file's version history, as the desktop's History menu shows it
 * (frontend ArtifactHistory). The daemon keeps up to 50 versions per file,
 * newest first; the first one is the file's current content.
 */

import { renderThemeRule, type RenderTheme } from "./chat/renderFrameModel";

/** One kept version of an artifact file. */
export type ArtifactVersionSummary = {
	version: number;
	size: number;
	/** Who wrote it: the agent, or the user through a save or restore. */
	source: "agent" | "user";
	/** The version a restore wrote back; absent otherwise. */
	restoredFrom?: number;
	/** Best effort: the agent running when the version was recorded. */
	harness?: string;
	createdAt: string;
};

/** One version with its text, cut to the first 256 KiB when `truncated`. */
export type ArtifactVersion = ArtifactVersionSummary & { content: string; truncated: boolean };

/** Reads the wire `versions` list, dropping anything malformed. */
export function parseVersionSummaries(raw: unknown): ArtifactVersionSummary[] {
	if (!Array.isArray(raw)) return [];
	return raw.flatMap((item) => {
		const summary = parseVersionSummary(item);
		return summary ? [summary] : [];
	});
}

export function parseVersionSummary(raw: unknown): ArtifactVersionSummary | undefined {
	if (!raw || typeof raw !== "object") return undefined;
	const { version, size, source, restoredFrom, harness, createdAt } = raw as Record<string, unknown>;
	if (typeof version !== "number" || !Number.isInteger(version) || version < 1) return undefined;
	return {
		version,
		size: typeof size === "number" && size >= 0 ? size : 0,
		source: source === "user" ? "user" : "agent",
		...(typeof restoredFrom === "number" && restoredFrom > 0 ? { restoredFrom } : {}),
		...(typeof harness === "string" && harness ? { harness } : {}),
		createdAt: typeof createdAt === "string" ? createdAt : "",
	};
}

/** One version's text, from the version read route. */
export function parseVersion(raw: unknown): ArtifactVersion | undefined {
	const summary = parseVersionSummary(raw);
	if (!summary) return undefined;
	const { content, truncated } = raw as Record<string, unknown>;
	return { ...summary, content: typeof content === "string" ? content : "", truncated: truncated === true };
}

const DAY_MS = 86_400_000;

/**
 * "Today, 8:31 PM", "Yesterday, 8:31 PM", else "Oct 11, 8:31 PM" (with the
 * year when it is not this one). Written out rather than through
 * Intl.RelativeTimeFormat, which Hermes does not have.
 */
export function formatVersionTime(isoDate: string, now: Date = new Date(), locale?: string): string {
	const date = new Date(isoDate);
	if (Number.isNaN(date.getTime())) return "";
	const time = date.toLocaleTimeString(locale, { hour: "numeric", minute: "2-digit" });
	const dayStart = (value: Date) => new Date(value.getFullYear(), value.getMonth(), value.getDate()).getTime();
	const days = Math.round((dayStart(now) - dayStart(date)) / DAY_MS);
	const day = days === 0
		? "Today"
		: days === 1
			? "Yesterday"
			: date.toLocaleDateString(locale, { day: "numeric", month: "short", ...(date.getFullYear() === now.getFullYear() ? {} : { year: "numeric" }) });
	return `${day}, ${time}`;
}

/**
 * "Restored from Today, 8:31 PM" for a version a restore wrote; the version it
 * came from may have been pruned since.
 */
export function restoredNote(version: ArtifactVersionSummary, versions: readonly ArtifactVersionSummary[], now?: Date, locale?: string): string | undefined {
	if (!version.restoredFrom) return undefined;
	const source = versions.find((candidate) => candidate.version === version.restoredFrom);
	return source ? `Restored from ${formatVersionTime(source.createdAt, now, locale)}` : "Restored from an earlier version";
}

/** Who wrote a version: the user, or an agent (the session's, when the version predates the record). */
export function versionAuthor(version: ArtifactVersionSummary, fallbackHarness?: string | null): { kind: "user" } | { kind: "agent"; harness?: string } {
	if (version.source === "user") return { kind: "user" };
	return { kind: "agent", harness: version.harness || fallbackHarness || undefined };
}

/**
 * The daemon's base page style (backend/internal/renderpage baseCSS), which
 * pages written for AO expect. The page's own CSS overrides it. A test reads
 * the Go constant and fails when the two drift apart.
 */
export const BASE_PAGE_CSS = "html{background:var(--background);color:var(--foreground);font-family:var(--font-sans);font-size:14px;line-height:1.5;-webkit-font-smoothing:antialiased;scrollbar-width:none}html::-webkit-scrollbar{display:none}body{margin:0}code,kbd,pre,samp{font-family:var(--font-mono)}"
	+ ":root{--viz-series-1:var(--chart-1);--viz-series-2:var(--chart-2);--viz-series-3:var(--chart-3);--viz-series-4:var(--chart-4);--viz-series-5:var(--chart-5);--viz-series-6:var(--chart-6);--viz-text:var(--foreground);--viz-muted:var(--muted-foreground);--viz-bg:var(--background);--viz-panel:var(--card);--viz-border:var(--border);--viz-accent:var(--accent);--viz-accent-text:var(--accent-foreground);--viz-accent-bg:var(--muted);--viz-warning:var(--warning);--blue:var(--chart-1);--green:var(--success);--orange:var(--warning);--yellow:var(--chart-3);--purple:var(--chart-4);--red:var(--destructive)}";

// A BOM, whitespace and comments may precede the doctype.
const LEADING_DOCTYPE = /^\uFEFF?(?:\s|<!--[\s\S]*?-->)*<!doctype[^>]*>/i;
const PAGE_VIEWPORT = /<meta\s[^>]*name\s*=\s*["']?viewport/i;

/**
 * A saved HTML version as a page, styled as the daemon serves the live file:
 * AO's theme variables (the app's own, as a frame gets them), the base style,
 * and a phone viewport when the page has none, written in after the doctype
 * (before it, the page would drop into quirks mode). A version is stored raw,
 * and the daemon only writes this in for the current file.
 */
export function versionPageHtml(content: string, theme: RenderTheme): string {
	const head = `<style id="ao-theme">${renderThemeRule(theme)}</style><style>${BASE_PAGE_CSS}</style>`
		+ (PAGE_VIEWPORT.test(content) ? "" : '<meta name="viewport" content="width=device-width, initial-scale=1">');
	const doctype = LEADING_DOCTYPE.exec(content);
	return doctype ? doctype[0] + head + content.slice(doctype[0].length) : `<!doctype html>${head}${content}`;
}
