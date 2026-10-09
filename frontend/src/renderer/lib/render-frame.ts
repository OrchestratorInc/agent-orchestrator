import type { ArtifactRef, ConversationActivity, RenderRef } from "../types/conversation";

export const RENDER_MIN_HEIGHT = 80;
export const RENDER_MAX_HEIGHT = 2000;
const RENDER_PATH = /^\/api\/v1\/sessions\/[^/]+\/renders\/[^/]+$/;
const ARTIFACT_URL = /^(\/api\/v1\/sessions\/[^/]+\/artifact-files\/).+/;
const MAX_MEASURED_WIDTHS = 16;

export function clampRenderHeight(height: number): number {
	return Math.min(RENDER_MAX_HEIGHT, Math.max(RENDER_MIN_HEIGHT, Math.round(height)));
}

/** The page a `render` activity points at, or undefined for anything malformed. */
export function readRenderRef(detail: ConversationActivity["detail"]): RenderRef | undefined {
	const render = detail?.event === "render" ? detail.render : undefined;
	if (
		!render ||
		typeof render.id !== "string" ||
		typeof render.title !== "string" ||
		typeof render.height !== "number" ||
		!Number.isFinite(render.height) ||
		typeof render.path !== "string" ||
		!RENDER_PATH.test(render.path)
	) {
		return undefined;
	}
	const ref: RenderRef = { id: render.id, title: render.title, height: clampRenderHeight(render.height), path: render.path };
	// A malformed measurement costs only the measured first height, not the page.
	if (isMeasuredHeights(render.heights)) ref.heights = render.heights;
	return ref;
}

/** The HTML artifact an `artifact` activity points at, or undefined for anything malformed. */
export function readArtifactRef(detail: ConversationActivity["detail"]): ArtifactRef | undefined {
	const artifact = detail?.event === "artifact" ? detail.artifact : undefined;
	if (
		!artifact ||
		typeof artifact.path !== "string" ||
		!isRelativePath(artifact.path) ||
		typeof artifact.name !== "string" ||
		typeof artifact.url !== "string" ||
		!staysUnder(artifact.url, ARTIFACT_URL.exec(artifact.url)?.[1])
	) {
		return undefined;
	}
	return { path: artifact.path, name: artifact.name, url: artifact.url };
}

/** Whether url, once the browser resolves its dot segments (escaped ones too), is still under prefix. */
function staysUnder(url: string, prefix: string | undefined): boolean {
	return prefix !== undefined && new URL(url, "http://127.0.0.1").pathname.startsWith(prefix);
}

/** Non-empty, not rooted or drive-lettered, and no `..` segment. */
function isRelativePath(path: string): boolean {
	return path !== "" && !/^([a-z]:|[\\/])/i.test(path) && !path.split(/[\\/]/).includes("..");
}

const isPositiveInteger = (value: unknown) => typeof value === "number" && Number.isInteger(value) && value > 0;

/** 1-16 `[width, height]` pairs of positive integers, in increasing width. */
function isMeasuredHeights(heights: unknown): heights is Array<[number, number]> {
	return (
		Array.isArray(heights) &&
		heights.length > 0 &&
		heights.length <= MAX_MEASURED_WIDTHS &&
		heights.every(
			(pair, index) =>
				Array.isArray(pair) &&
				pair.length === 2 &&
				isPositiveInteger(pair[0]) &&
				isPositiveInteger(pair[1]) &&
				(index === 0 || pair[0] > heights[index - 1][0]),
		)
	);
}

/**
 * The page's height at a frame width, from its measured heights: the taller of
 * the heights at the nearest measured widths on each side, since a breakpoint
 * between them can make the page as tall as either. Ported from T3 Code's
 * `measuredHeight` (packages/shared/src/htmlRender.ts).
 */
export function measuredRenderHeight(heights: Array<[number, number]>, width: number): number {
	const above = heights.findIndex(([measuredWidth]) => measuredWidth >= width);
	const high = above === -1 ? heights.length - 1 : above;
	const low = heights[high]![0] === width ? high : Math.max(0, high - 1);
	return Math.max(heights[low]![1], heights[high]![1]);
}

/**
 * The file a saved render is written to: its title, without characters a file
 * system refuses, capped at 120. Ported from T3 Code's `htmlRenderFileName`
 * (packages/shared/src/htmlRender.ts).
 */
export function renderFileName(title: string): string {
	const name = title
		.replace(/[\\/:*?"<>|\p{Cc}]+/gu, " ")
		.replace(/\s+/g, " ")
		.trim()
		.slice(0, 120)
		.trim();
	return `${name || "Page"}.html`;
}

export interface RenderTheme {
	appearance: "light" | "dark";
	variables: Record<string, string>;
}

// Agent-facing names (documented in commands/render.md) -> AO's semantic tokens
// in frontend/src/styles/tokens.css. getComputedStyle resolves their var() chains.
const THEME_TOKENS: ReadonlyArray<readonly [string, string]> = [
	["--background", "--color-bg-primary"],
	["--foreground", "--color-text-primary"],
	["--muted", "--color-bg-tertiary"],
	["--muted-foreground", "--color-text-muted"],
	["--card", "--color-bg-secondary"],
	["--card-foreground", "--color-text-primary"],
	["--popover", "--color-bg-elevated"],
	["--border", "--color-border"],
	["--border-strong", "--color-border-strong"],
	["--primary", "--color-accent"],
	["--primary-foreground", "--color-accent-foreground"],
	["--accent", "--color-accent"],
	["--accent-foreground", "--color-accent-foreground"],
	["--success", "--color-success"],
	["--warning", "--color-warning"],
	["--destructive", "--color-danger"],
	["--code", "--color-text-markdown-code"],
	["--link", "--color-text-markdown-link"],
	["--chart-1", "--color-brand-logo"],
	["--font-sans", "--font-family-base"],
	["--font-mono", "--font-family-mono"],
	["--radius", "--radius-md"],
];

// ponytail: fixed categorical series for agent pages only (never AO chrome);
// AO's own --chart-* tokens are grayscale. Swap for tokens once design names some.
const CHART_SERIES = {
	dark: ["#2dd4bf", "#fbbf24", "#c084fc", "#fb7185", "#a3e635"],
	light: ["#0d9488", "#d97706", "#9333ea", "#e11d48", "#65a30d"],
} as const;

export function readRenderTheme(root: HTMLElement = document.documentElement): RenderTheme {
	const appearance = root.getAttribute("data-theme") === "light" ? "light" : "dark";
	const style = getComputedStyle(root);
	const variables: Record<string, string> = {};
	for (const [name, token] of THEME_TOKENS) {
		const value = style.getPropertyValue(token).trim();
		if (value) variables[name] = value;
	}
	CHART_SERIES[appearance].forEach((color, index) => {
		variables[`--chart-${index + 2}`] = color;
	});
	return { appearance, variables };
}

export function renderThemesEqual(left: RenderTheme, right: RenderTheme): boolean {
	return left.appearance === right.appearance && JSON.stringify(left.variables) === JSON.stringify(right.variables);
}

/** Where the page shows: inline in the thread, or expanded in the dialog. */
export type RenderDisplayMode = "inline" | "fullscreen";

/** URL fragment that hands a render its theme and display mode before first paint. */
/**
 * The fragment a render reads its theme from before first paint. scrollable is
 * AO's own field: the page scrolls inside a capped frame, so it shows its
 * scrollbar.
 */
export function renderThemeFragment(theme: RenderTheme, displayMode: RenderDisplayMode, scrollable = false): string {
	return `#ao-theme=${encodeURIComponent(JSON.stringify({ ...theme, displayMode, ...(scrollable ? { scrollable } : {}) }))}`;
}

/** The message a mounted render restyles from when the theme changes; displayMode is the MCP Apps host-context field. */
export function renderThemeMessage(theme: RenderTheme, displayMode: RenderDisplayMode, scrollable = false) {
	return {
		jsonrpc: "2.0",
		method: "ui/notifications/host-context-changed",
		params: { theme: theme.appearance, styles: { variables: theme.variables }, displayMode, ...(scrollable ? { scrollable } : {}) },
	} as const;
}

function rpc(data: unknown, method: string): Record<string, unknown> | undefined {
	if (typeof data !== "object" || data === null) return undefined;
	const message = data as Record<string, unknown>;
	if (message.jsonrpc !== "2.0" || message.method !== method) return undefined;
	return typeof message.params === "object" && message.params !== null ? (message.params as Record<string, unknown>) : undefined;
}

export function readRenderContentHeight(data: unknown): number | undefined {
	const height = rpc(data, "ui/notifications/size-changed")?.height;
	return typeof height === "number" && Number.isFinite(height) && height > 0 ? height : undefined;
}

export function readRenderLinkRequest(data: unknown): string | undefined {
	const url = rpc(data, "ui/open-link")?.url;
	return typeof url === "string" && /^https?:\/\//i.test(url) ? url : undefined;
}
