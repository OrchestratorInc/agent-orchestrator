import type { ITheme } from "@xterm/xterm";

function cssVar(name: string): string {
	if (typeof document === "undefined") return "";
	return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
}

export function buildTerminalThemes(translucent = false): { dark: ITheme; light: ITheme } {
	const namedThemeActive = typeof document !== "undefined" && Boolean(document.documentElement.dataset.styleTheme);
	const terminalBg = namedThemeActive
		? cssVar("--background")
		: cssVar("--color-bg-terminal-opaque") || cssVar("--color-bg-terminal");
	const terminalForeground = namedThemeActive ? cssVar("--foreground") : cssVar("--color-text-terminal");
	const terminalCursor = namedThemeActive ? cssVar("--primary") : cssVar("--color-working");
	const ansiBlack = terminalBg;
	const dark: ITheme = {
		background: translucent ? "#00000000" : terminalBg,
		overviewRulerBorder: "transparent",
		foreground: terminalForeground,
		cursor: terminalCursor,
		cursorAccent: terminalBg,
		selectionBackground: cssVar("--color-term-selection-dark"),
		selectionInactiveBackground: cssVar("--color-term-selection-inactive"),
		black: ansiBlack,
		red: cssVar("--color-term-red"),
		green: cssVar("--color-term-green"),
		yellow: cssVar("--color-term-yellow"),
		blue: cssVar("--color-term-blue"),
		magenta: cssVar("--color-term-magenta"),
		cyan: cssVar("--color-term-cyan"),
		white: cssVar("--color-term-white"),
		brightBlack: cssVar("--color-term-bright-black"),
		brightRed: cssVar("--color-term-bright-red"),
		brightGreen: cssVar("--color-term-bright-green"),
		brightYellow: cssVar("--color-term-bright-yellow"),
		brightBlue: cssVar("--color-term-bright-blue"),
		brightMagenta: cssVar("--color-term-bright-magenta"),
		brightCyan: cssVar("--color-term-bright-cyan"),
		brightWhite: cssVar("--color-term-bright-white"),
	};

	const light: ITheme = {
		background: translucent ? "#00000000" : terminalBg,
		overviewRulerBorder: "transparent",
		foreground: terminalForeground,
		cursor: terminalForeground,
		cursorAccent: terminalBg,
		selectionBackground: cssVar("--color-term-selection-light"),
		selectionInactiveBackground: cssVar("--color-term-selection-inactive-light"),
		black: ansiBlack,
		red: cssVar("--color-term-red"),
		green: cssVar("--color-term-green"),
		yellow: cssVar("--color-term-yellow"),
		blue: cssVar("--color-term-blue"),
		magenta: cssVar("--color-term-magenta"),
		cyan: cssVar("--color-term-cyan"),
		white: cssVar("--color-term-white"),
		brightBlack: cssVar("--color-term-bright-black"),
		brightRed: cssVar("--color-term-bright-red"),
		brightGreen: cssVar("--color-term-bright-green"),
		brightYellow: cssVar("--color-term-bright-yellow"),
		brightBlue: cssVar("--color-term-bright-blue"),
		brightMagenta: cssVar("--color-term-bright-magenta"),
		brightCyan: cssVar("--color-term-bright-cyan"),
		brightWhite: cssVar("--color-term-bright-white"),
	};

	return { dark, light };
}
