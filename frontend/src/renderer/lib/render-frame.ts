import type { ConversationActivity, RenderRef } from "../types/conversation";

export const RENDER_MIN_HEIGHT = 80;
export const RENDER_MAX_HEIGHT = 2000;
const RENDER_PATH = /^\/api\/v1\/sessions\/[^/]+\/renders\/[^/]+$/;

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
	return { id: render.id, title: render.title, height: clampRenderHeight(render.height), path: render.path };
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

/** URL fragment that hands a render its theme before first paint. */
export function renderThemeFragment(theme: RenderTheme): string {
	return `#ao-theme=${encodeURIComponent(JSON.stringify(theme))}`;
}

/** The message a mounted render restyles from when the theme changes. */
export function renderThemeMessage(theme: RenderTheme) {
	return {
		jsonrpc: "2.0",
		method: "ui/notifications/host-context-changed",
		params: { theme: theme.appearance, styles: { variables: theme.variables } },
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
