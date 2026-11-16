/**
 * Named color themes, shared by the desktop and mobile apps. Every theme ships
 * a dark and a light palette; values are the theme's canonical colors, nudged
 * only where a role failed the contrast floor in
 * frontend/src/renderer/lib/theme-palettes.test.ts. Each app derives its own
 * tokens from these roles: desktop in frontend/src/renderer/lib/theme-tokens.ts,
 * mobile in packages/mobile/lib/themeDerive.ts.
 */

export type ThemePalette = {
	/** Main canvas. */
	bg: string;
	/** Sidebar rail; usually one step darker than `bg` in dark mode. */
	sidebar: string;
	/** Raised cards and grouped content. */
	panel: string;
	/** Hover, selected and input sub-surfaces. */
	element: string;
	text: string;
	/** Secondary text; must stay readable (WCAG AA) on `bg`. */
	muted: string;
	/** Links, focus ring, selection, brand mark. */
	accent: string;
	/** Primary button fill when it differs from `accent` (e.g. Vercel's white button). */
	primary?: string;
	red: string;
	orange: string;
	yellow: string;
	green: string;
	blue: string;
	purple: string;
	cyan: string;
};

export type ThemeDefinition = {
	id: string;
	label: string;
	dark: ThemePalette;
	light: ThemePalette;
};

export const THEMES = [
	{
		// Owned by tokens.css on desktop and by the hand-tuned palette in
		// packages/mobile/lib/theme.ts; these values only feed the picker swatch.
		id: "orchestrate",
		label: "Orchestrate",
		dark: {
			bg: "#181819", sidebar: "#111212", panel: "#212223", element: "#2c2d2d",
			text: "#f4f5f6", muted: "#a4a5a6", accent: "#79b0dc", primary: "#dddedf",
			red: "#ff6467", orange: "#f06445", yellow: "#facc15", green: "#4ade80",
			blue: "#60a5fa", purple: "#c084fc", cyan: "#56b6c2",
		},
		light: {
			bg: "#f5f5f5", sidebar: "#eeeeee", panel: "#ffffff", element: "#e9e9e9",
			text: "#121212", muted: "#636363", accent: "#304c83", primary: "#1f1f1f",
			red: "#e7000b", orange: "#c2412d", yellow: "#ca8a04", green: "#16a34a",
			blue: "#2563eb", purple: "#9333ea", cyan: "#3d7a7a",
		},
	},
	{
		id: "vercel",
		label: "Vercel",
		dark: {
			bg: "#0a0a0a", sidebar: "#000000", panel: "#111111", element: "#1f1f1f",
			text: "#ededed", muted: "#a1a1a1", accent: "#52a8ff", primary: "#ededed",
			red: "#ff6166", orange: "#ff990a", yellow: "#ffb224", green: "#63c46d",
			blue: "#52a8ff", purple: "#bf7af0", cyan: "#0ac7b4",
		},
		light: {
			bg: "#ffffff", sidebar: "#fafafa", panel: "#fafafa", element: "#ebebeb",
			text: "#171717", muted: "#666666", accent: "#0068d6", primary: "#171717",
			red: "#cb2a2f", orange: "#bd4b00", yellow: "#a35200", green: "#297a3a",
			blue: "#0068d6", purple: "#7d3cba", cyan: "#067a6e",
		},
	},
	{
		id: "github",
		label: "GitHub",
		dark: {
			bg: "#0d1117", sidebar: "#010409", panel: "#151b23", element: "#262c36",
			text: "#e6edf3", muted: "#9198a1", accent: "#4493f8",
			red: "#f85149", orange: "#db6d28", yellow: "#d29922", green: "#3fb950",
			blue: "#58a6ff", purple: "#ab7df8", cyan: "#39c5cf",
		},
		light: {
			bg: "#ffffff", sidebar: "#f6f8fa", panel: "#f6f8fa", element: "#eaeef2",
			text: "#1f2328", muted: "#59636e", accent: "#0969da",
			red: "#d1242f", orange: "#bc4c00", yellow: "#9a6700", green: "#1a7f37",
			blue: "#0969da", purple: "#8250df", cyan: "#1b7c83",
		},
	},
	{
		id: "one",
		label: "One",
		dark: {
			bg: "#282c34", sidebar: "#21252b", panel: "#2c313a", element: "#3a3f4b",
			text: "#c8ccd4", muted: "#9da5b4", accent: "#61afef",
			red: "#e06c75", orange: "#d19a66", yellow: "#e5c07b", green: "#98c379",
			blue: "#61afef", purple: "#c678dd", cyan: "#56b6c2",
		},
		light: {
			bg: "#fafafa", sidebar: "#eaeaeb", panel: "#f2f2f2", element: "#e5e5e6",
			text: "#383a42", muted: "#696c77", accent: "#4078f2",
			red: "#e45649", orange: "#986801", yellow: "#986801", green: "#50a14f",
			blue: "#4078f2", purple: "#a626a4", cyan: "#0184bc",
		},
	},
	{
		id: "catppuccin",
		label: "Catppuccin",
		dark: {
			bg: "#1e1e2e", sidebar: "#181825", panel: "#24243a", element: "#313244",
			text: "#cdd6f4", muted: "#a6adc8", accent: "#cba6f7",
			red: "#f38ba8", orange: "#fab387", yellow: "#f9e2af", green: "#a6e3a1",
			blue: "#89b4fa", purple: "#cba6f7", cyan: "#94e2d5",
		},
		light: {
			bg: "#eff1f5", sidebar: "#e6e9ef", panel: "#f5f6f9", element: "#dce0e8",
			text: "#474a63", muted: "#696c81", accent: "#8839ef",
			red: "#d20f39", orange: "#f15800", yellow: "#c77800", green: "#3d9d28",
			blue: "#1e66f5", purple: "#8839ef", cyan: "#179299",
		},
	},
	{
		id: "tokyo-night",
		label: "Tokyo Night",
		dark: {
			bg: "#1a1b26", sidebar: "#16161e", panel: "#1f2335", element: "#292e42",
			text: "#c0caf5", muted: "#9aa5ce", accent: "#7aa2f7",
			red: "#f7768e", orange: "#ff9e64", yellow: "#e0af68", green: "#9ece6a",
			blue: "#7aa2f7", purple: "#bb9af7", cyan: "#7dcfff",
		},
		light: {
			bg: "#e1e2e7", sidebar: "#d5d6db", panel: "#e9e9ed", element: "#c4c8da",
			text: "#153894", muted: "#50609d", accent: "#2e7de9",
			red: "#f42864", orange: "#b15c00", yellow: "#8c6c3e", green: "#587539",
			blue: "#2e7de9", purple: "#9854f1", cyan: "#007197",
		},
	},
	{
		id: "dracula",
		label: "Dracula",
		dark: {
			bg: "#282a36", sidebar: "#21222c", panel: "#2e303e", element: "#44475a",
			text: "#f8f8f2", muted: "#a7acc8", accent: "#bd93f9",
			red: "#ff5555", orange: "#ffb86c", yellow: "#f1fa8c", green: "#50fa7b",
			blue: "#8be9fd", purple: "#bd93f9", cyan: "#8be9fd",
		},
		// Alucard, Dracula's official light variant.
		light: {
			bg: "#fffbeb", sidebar: "#f5f0dc", panel: "#fffdf4", element: "#ecead8",
			text: "#1f1f1f", muted: "#6c664b", accent: "#644ac9",
			red: "#cb3a2a", orange: "#a34d14", yellow: "#846e15", green: "#14710a",
			blue: "#036a96", purple: "#644ac9", cyan: "#036a96",
		},
	},
	{
		id: "nord",
		label: "Nord",
		dark: {
			bg: "#2e3440", sidebar: "#272c36", panel: "#343a47", element: "#3b4252",
			text: "#eceff4", muted: "#a5afc2", accent: "#88c0d0",
			red: "#bf616a", orange: "#d08770", yellow: "#ebcb8b", green: "#a3be8c",
			blue: "#81a1c1", purple: "#b48ead", cyan: "#8fbcbb",
		},
		// Nord has no official light variant: Snow Storm surfaces, Polar Night
		// text, Frost accent, and Aurora hues darkened for light-plate contrast.
		light: {
			bg: "#eceff4", sidebar: "#e5e9f0", panel: "#f4f6f9", element: "#d8dee9",
			text: "#2e3440", muted: "#4c566a", accent: "#5e81ac",
			red: "#a5434c", orange: "#a3593b", yellow: "#8a6d1f", green: "#4f7a3a",
			blue: "#5e81ac", purple: "#8a5f82", cyan: "#3b7f86",
		},
	},
	{
		id: "gruvbox",
		label: "Gruvbox",
		dark: {
			bg: "#282828", sidebar: "#1d2021", panel: "#32302f", element: "#3c3836",
			text: "#ebdbb2", muted: "#a89984", accent: "#83a598",
			red: "#fb4934", orange: "#fe8019", yellow: "#fabd2f", green: "#b8bb26",
			blue: "#83a598", purple: "#d3869b", cyan: "#8ec07c",
		},
		light: {
			bg: "#fbf1c7", sidebar: "#f2e5bc", panel: "#f9f5d7", element: "#ebdbb2",
			text: "#3c3836", muted: "#776a5f", accent: "#076678",
			red: "#9d0006", orange: "#af3a03", yellow: "#b57614", green: "#79740e",
			blue: "#076678", purple: "#8f3f71", cyan: "#427b58",
		},
	},
	{
		id: "rose-pine",
		label: "Rosé Pine",
		dark: {
			bg: "#191724", sidebar: "#15131f", panel: "#1f1d2e", element: "#26233a",
			text: "#e0def4", muted: "#908caa", accent: "#c4a7e7",
			red: "#eb6f92", orange: "#ebbcba", yellow: "#f6c177", green: "#9ccfd8",
			blue: "#569fba", purple: "#c4a7e7", cyan: "#9ccfd8",
		},
		// Dawn.
		light: {
			bg: "#faf4ed", sidebar: "#f4ede8", panel: "#fffaf3", element: "#f2e9e1",
			text: "#4f4a71", muted: "#706c89", accent: "#907aa9",
			red: "#b4637a", orange: "#ca7672", yellow: "#c77d00", green: "#56949f",
			blue: "#286983", purple: "#907aa9", cyan: "#56949f",
		},
	},
	{
		id: "solarized",
		label: "Solarized",
		dark: {
			bg: "#002b36", sidebar: "#00212b", panel: "#04313c", element: "#073642",
			text: "#c5cdcd", muted: "#93a1a1", accent: "#268bd2",
			red: "#dc322f", orange: "#cb4b16", yellow: "#b58900", green: "#859900",
			blue: "#268bd2", purple: "#6c71c4", cyan: "#2aa198",
		},
		light: {
			bg: "#fdf6e3", sidebar: "#eee8d5", panel: "#fefaf0", element: "#eee8d5",
			text: "#073642", muted: "#586e75", accent: "#268bd2",
			red: "#dc322f", orange: "#cb4b16", yellow: "#b28700", green: "#839700",
			blue: "#268bd2", purple: "#6c71c4", cyan: "#249d94",
		},
	},
	{
		id: "kanagawa",
		label: "Kanagawa",
		// Wave.
		dark: {
			bg: "#1f1f28", sidebar: "#16161d", panel: "#2a2a37", element: "#363646",
			text: "#dcd7ba", muted: "#a6a69c", accent: "#7e9cd8",
			red: "#e46876", orange: "#ffa066", yellow: "#e6c384", green: "#98bb6c",
			blue: "#7e9cd8", purple: "#957fb8", cyan: "#7aa89f",
		},
		// Lotus.
		light: {
			bg: "#f2ecbc", sidebar: "#e7dba0", panel: "#f5f0cc", element: "#e5ddb0",
			text: "#424251", muted: "#6c695c", accent: "#4d699b",
			red: "#c84053", orange: "#cc6d00", yellow: "#77713f", green: "#6f894e",
			blue: "#4d699b", purple: "#624c83", cyan: "#597b75",
		},
	},
	{
		id: "vscode",
		label: "VS Code",
		// Dark Modern / Light Modern.
		dark: {
			bg: "#1f1f1f", sidebar: "#181818", panel: "#252526", element: "#313131",
			text: "#cccccc", muted: "#9d9d9d", accent: "#4daafc", primary: "#0078d4",
			red: "#f85149", orange: "#d18616", yellow: "#cca700", green: "#2ea043",
			blue: "#4daafc", purple: "#b180d7", cyan: "#29b8db",
		},
		light: {
			bg: "#ffffff", sidebar: "#f8f8f8", panel: "#f8f8f8", element: "#e5e5e5",
			text: "#3b3b3b", muted: "#6e6e6e", accent: "#005fb8",
			red: "#cd3131", orange: "#b5520a", yellow: "#895503", green: "#107c10",
			blue: "#005fb8", purple: "#8a46c9", cyan: "#0e7490",
		},
	},
	{
		id: "monokai",
		label: "Monokai",
		// Monokai Pro / Monokai Pro Light.
		dark: {
			bg: "#2d2a2e", sidebar: "#221f22", panel: "#353236", element: "#403e41",
			text: "#fcfcfa", muted: "#a9a8a9", accent: "#ffd866",
			red: "#ff6188", orange: "#fc9867", yellow: "#ffd866", green: "#a9dc76",
			blue: "#78dce8", purple: "#ab9df2", cyan: "#78dce8",
		},
		light: {
			bg: "#faf4f2", sidebar: "#ede7e5", panel: "#fdf9f8", element: "#e6e0de",
			text: "#29242a", muted: "#706b6e", accent: "#cc7a0a",
			red: "#e14775", orange: "#e16032", yellow: "#cc7a0a", green: "#269d69",
			blue: "#1c8ca8", purple: "#7058be", cyan: "#1c8ca8",
		},
	},
	{
		id: "material",
		label: "Material",
		// Darker / Lighter.
		dark: {
			bg: "#212121", sidebar: "#1a1a1a", panel: "#292929", element: "#323232",
			text: "#eeffff", muted: "#b0bec5", accent: "#80cbc4",
			red: "#f07178", orange: "#f78c6c", yellow: "#ffcb6b", green: "#c3e88d",
			blue: "#82aaff", purple: "#c792ea", cyan: "#89ddff",
		},
		light: {
			bg: "#fafafa", sidebar: "#eeeeee", panel: "#ffffff", element: "#e7e7e8",
			text: "#37474f", muted: "#546e7a", accent: "#00897b",
			red: "#e53935", orange: "#ee653f", yellow: "#cd7e00", green: "#779c3d",
			blue: "#6182b8", purple: "#7c4dff", cyan: "#259fa7",
		},
	},
	{
		id: "ayu",
		label: "Ayu",
		// Mirage / Light.
		dark: {
			bg: "#1f2430", sidebar: "#1c212b", panel: "#232834", element: "#2d3443",
			text: "#cccac2", muted: "#959ca8", accent: "#ffcc66",
			red: "#f28779", orange: "#ffad66", yellow: "#ffd173", green: "#d5ff80",
			blue: "#73d0ff", purple: "#dfbfff", cyan: "#95e6cb",
		},
		light: {
			bg: "#fcfcfc", sidebar: "#f3f4f5", panel: "#ffffff", element: "#e7eaed",
			text: "#4e5357", muted: "#717479", accent: "#db780d",
			red: "#e65050", orange: "#e0751e", yellow: "#c68409", green: "#74a000",
			blue: "#3198df", purple: "#a37acc", cyan: "#2aa480",
		},
	},
	{
		id: "night-owl",
		label: "Night Owl",
		dark: {
			bg: "#011627", sidebar: "#01111d", panel: "#0b2942", element: "#1d3b53",
			text: "#d6deeb", muted: "#8ca2b8", accent: "#82aaff",
			red: "#ef5350", orange: "#f78c6c", yellow: "#ecc48d", green: "#addb67",
			blue: "#82aaff", purple: "#c792ea", cyan: "#7fdbca",
		},
		// Light Owl.
		light: {
			bg: "#fbfbfb", sidebar: "#f0f0f0", panel: "#ffffff", element: "#e0e7ea",
			text: "#403f53", muted: "#6a7179", accent: "#4876d6",
			red: "#de3d3b", orange: "#aa6c02", yellow: "#b98900", green: "#08916a",
			blue: "#288ed7", purple: "#994cc3", cyan: "#28a197",
		},
	},
	{
		id: "everforest",
		label: "Everforest",
		dark: {
			bg: "#2d353b", sidebar: "#232a2e", panel: "#343f44", element: "#3d484d",
			text: "#ddd0b4", muted: "#9da9a0", accent: "#a7c080",
			red: "#e67e80", orange: "#e69875", yellow: "#dbbc7f", green: "#a7c080",
			blue: "#7fbbb3", purple: "#d699b6", cyan: "#83c092",
		},
		light: {
			bg: "#fdf6e3", sidebar: "#efebd4", panel: "#f4f0d9", element: "#e6e2cc",
			text: "#414e56", muted: "#667465", accent: "#849700",
			red: "#f85552", orange: "#e36c02", yellow: "#bf8200", green: "#849700",
			blue: "#3a94c5", purple: "#d964b5", cyan: "#2b9f75",
		},
	},
	{
		id: "vitesse",
		label: "Vitesse",
		dark: {
			bg: "#121212", sidebar: "#0d0d0d", panel: "#181818", element: "#232323",
			text: "#dbd7ca", muted: "#959da5", accent: "#4d9375",
			red: "#cb7676", orange: "#d4976c", yellow: "#e6cc77", green: "#4d9375",
			blue: "#6394bf", purple: "#d9739f", cyan: "#5eaab5",
		},
		light: {
			bg: "#ffffff", sidebar: "#f7f7f7", panel: "#fafafa", element: "#eeeeee",
			text: "#393a34", muted: "#6b6b6b", accent: "#1c6b48",
			red: "#ab5959", orange: "#a65e2b", yellow: "#8c6a10", green: "#1e754f",
			blue: "#296aa3", purple: "#a13865", cyan: "#2f798a",
		},
	},
	{
		id: "flexoki",
		label: "Flexoki",
		dark: {
			bg: "#100f0f", sidebar: "#0a0909", panel: "#1c1b1a", element: "#282726",
			text: "#cecdc3", muted: "#878580", accent: "#da702c",
			red: "#d14d41", orange: "#da702c", yellow: "#d0a215", green: "#879a39",
			blue: "#4385be", purple: "#8b7ec8", cyan: "#3aa99f",
		},
		light: {
			bg: "#fffcf0", sidebar: "#f2f0e5", panel: "#fffefa", element: "#e6e4d9",
			text: "#100f0f", muted: "#6f6e69", accent: "#bc5215",
			red: "#af3029", orange: "#bc5215", yellow: "#ad8301", green: "#66800b",
			blue: "#205ea6", purple: "#5e409d", cyan: "#24837b",
		},
	},
] as const satisfies readonly ThemeDefinition[];

export type ThemeStyle = (typeof THEMES)[number]["id"];

export const DEFAULT_THEME_STYLE: ThemeStyle = "orchestrate";

const THEME_IDS = new Set<string>(THEMES.map((theme) => theme.id));

export function isThemeStyle(value: unknown): value is ThemeStyle {
	return typeof value === "string" && THEME_IDS.has(value);
}

export function getThemeDefinition(id: ThemeStyle): ThemeDefinition {
	return THEMES.find((theme) => theme.id === id) ?? THEMES[0];
}
