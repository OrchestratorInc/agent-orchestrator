import { beforeEach, describe, expect, it } from "vitest";
import {
	buildCustomThemeCss,
	customThemeColors,
	customThemeHasAppearance,
	customThemePalette,
	customThemeStyle,
	customThemeSwitchStorageKey,
	isCustomThemeStyle,
	resolveCustomTheme,
	stripThemeDeclarations,
	takeCustomThemeSwitch,
	themeAppearanceSelector,
	themeCssValueFetches,
} from "./custom-theme";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { contrastRatio } from "./theme-color";
import { THEME_COLOR_ROLES, UNPAINTED_THEME_ROLES } from "./theme-file";
import { getThemeDefinition } from "./theme-palettes";

const dark = (colors: Record<string, string>, extra: Partial<{ variants: Record<string, Record<string, string>>; css: string }> = {}) => ({
	id: "sumi",
	appearance: "dark" as const,
	colors,
	variants: extra.variants ?? null,
	css: extra.css ?? "",
});

describe("customThemeColors", () => {
	it("reads the theme's own appearance from colors and the other from variants, as hex", () => {
		const theme = dark({ canvas: "oklch(1 0 0)", bogus: "#000", text: "not a color" }, { variants: { light: { canvas: "rgb(250 250 250)" } } });
		expect(customThemeColors(theme, "dark")).toEqual({ canvas: "#ffffff" });
		expect(customThemeColors(theme, "light")).toEqual({ canvas: "#fafafa" });
	});

	it("has no colors for an appearance the file leaves out", () => {
		const theme = dark({ canvas: "#101012" });
		expect(customThemeColors(theme, "light")).toBeNull();
		expect(customThemeHasAppearance(theme, "dark")).toBe(true);
		expect(customThemeHasAppearance(theme, "light")).toBe(false);
		expect(customThemeHasAppearance({ ...theme, appearance: "light" }, "light")).toBe(true);
	});
});

describe("resolveCustomTheme", () => {
	it("writes only the tokens behind the roles a theme sets", () => {
		const { root, frame, rows, message, code } = resolveCustomTheme({ accent: "#e0a84f" }, "dark");
		expect(root["--primary"]).toBe("#e0a84f");
		expect(root["--primary-foreground"]).toBe("#000000");
		expect(root["--color-text-markdown-link"]).toBe("#e0a84f");
		expect(root["--ring"]).toBe("#e0a84f");
		for (const untouched of ["--background", "--foreground", "--card", "--border", "--sidebar", "--muted", "--color-term-red"]) {
			expect(root).not.toHaveProperty(untouched);
		}
		expect(frame).toBeNull();
		expect(rows).toBeNull();
		expect(message).toBeNull();
		expect(code).toBeNull();
	});

	it("derives the rest of the look from canvas and text", () => {
		const { root, frame } = resolveCustomTheme({ canvas: "#0b1020", text: "#e6e9f5" }, "dark");
		expect(root["--background"]).toBe("#0b1020");
		expect(root["--foreground"]).toBe("#e6e9f5");
		for (const derived of ["--card", "--popover", "--secondary", "--muted", "--muted-foreground", "--border", "--input", "--sidebar", "--chart-3", "--color-term-black"]) {
			expect(root[derived]).toMatch(/^#[0-9a-f]{6,8}$/);
		}
		// The frame follows the canvas, one step darker.
		expect(root["--sidebar"]).not.toBe("#0b1020");
		expect(contrastRatio(root["--muted-foreground"], "#0b1020")).toBeGreaterThan(4.5);
		expect(frame?.["--color-text-primary"]).toBe("#e6e9f5");
	});

	it("keeps frame text readable when the sidebar and the canvas disagree", () => {
		// A Slack-style aubergine sidebar under a light theme: dark text would vanish.
		const light = resolveCustomTheme({ sidebar: "#3f0e40" }, "light");
		expect(light.root["--sidebar"]).toBe("#3f0e40");
		expect(light.frame?.color).toBe("#ffffff");
		expect(light.frame?.["--color-text-primary"]).toBe("#ffffff");
		expect(contrastRatio(light.frame?.["--muted-foreground"] ?? "#000000", "#3f0e40")).toBeGreaterThan(3);
		// The canvas keeps its own text.
		expect(light.root).not.toHaveProperty("--foreground");

		// In dark the default text already reads on it.
		const darkTheme = resolveCustomTheme({ sidebar: "#3f0e40" }, "dark");
		expect(darkTheme.frame?.color).toBe(getThemeDefinition("orchestrate").dark.text);

		// An explicit foreground wins.
		expect(resolveCustomTheme({ sidebar: "#3f0e40", sidebarForeground: "#ffd7ff" }, "light").frame?.color).toBe("#ffd7ff");
	});

	it("tells hovered and selected sidebar rows apart once a theme sets one", () => {
		const { rows, root, frame } = resolveCustomTheme({ sidebar: "#3f0e40", sidebarRowSelected: "#1164a3" }, "dark");
		expect(rows?.selected).toBe("#1164a3");
		expect(rows?.hover).not.toBe("#1164a3");
		expect(root["--sidebar-accent"]).toBe("#1164a3");
		expect(frame?.["--sidebar-accent"]).toBe("#1164a3");
		expect(resolveCustomTheme({ sidebar: "#3f0e40" }, "dark").rows).toBeNull();
	});

	it("uses chrome for the frame only when no sidebar is set", () => {
		expect(resolveCustomTheme({ chrome: "#111111" }, "dark").root["--sidebar"]).toBe("#111111");
		expect(resolveCustomTheme({ chrome: "#111111", sidebar: "#222222" }, "dark").root["--sidebar"]).toBe("#222222");
	});

	it("makes a dim accent readable as a link without changing the button fill", () => {
		const { root } = resolveCustomTheme({ canvas: "#0c0d10", accent: "#3a3f52" }, "dark");
		expect(root["--primary"]).toBe("#3a3f52");
		expect(contrastRatio(root["--color-text-markdown-link"], "#0c0d10")).toBeGreaterThanOrEqual(4.5);
	});

	it("paints the chat bubble, code blocks, placeholders and the terminal", () => {
		const resolved = resolveCustomTheme(
			{ messageSurface: "#1164a3", messageForeground: "#ffffff", codeBackground: "#000000", placeholder: "#777777", terminalBackground: "#001100", terminalForeground: "#00ff66", terminalCursor: "#00ff66", red: "#ff5555" },
			"dark",
		);
		expect(resolved.message).toEqual({ "background-color": "#1164a3", color: "#ffffff" });
		expect(resolved.code).toEqual({ "background-color": "#000000" });
		expect(resolved.placeholder).toBe("#777777");
		expect(resolved.root["--theme-terminal-background"]).toBe("#001100");
		expect(resolved.root["--color-bg-terminal"]).toBe("#001100");
		expect(resolved.root["--theme-terminal-foreground"]).toBe("#00ff66");
		expect(resolved.root["--theme-terminal-cursor"]).toBe("#00ff66");
		expect(resolved.root["--color-term-black"]).toBe("#001100");
		expect(resolved.root["--color-term-red"]).toBe("#ff5555");
		expect(resolved.root["--destructive"]).toBe("#ff5555");
	});
});

describe("painted and unpainted roles", () => {
	const paints = (role: string) => {
		const resolved = resolveCustomTheme({ [role]: "#123456" }, "dark");
		return Object.keys(resolved.root).length > 0 || resolved.frame !== null || resolved.message !== null || resolved.code !== null || resolved.placeholder !== undefined;
	};

	it("paints every role that is not listed as unpainted, and none that is", () => {
		const unpainted = new Set<string>(UNPAINTED_THEME_ROLES);
		for (const role of THEME_COLOR_ROLES) expect([role, paints(role)]).toEqual([role, !unpainted.has(role)]);
	});

	it("does not count unpainted roles as an appearance", () => {
		// Picking such a theme must not switch light or dark for no visible change.
		const theme = dark({ toolbar: "#111111", update: "#222222" });
		expect(customThemeHasAppearance(theme, "dark")).toBe(false);
		expect(buildCustomThemeCss(theme)).not.toContain("--");
	});

	it("names the same unpainted roles as the daemon", () => {
		const source = readFileSync(resolve(__dirname, "../../../../backend/internal/service/apptheme/apptheme.go"), "utf8");
		const block = source.slice(source.indexOf("var UnpaintedRoles = []string{"), source.indexOf("// Theme is one custom theme"));
		const daemon = Array.from(block.matchAll(/"([A-Za-z]+)"/g), (match) => match[1]);
		expect(daemon).toEqual([...UNPAINTED_THEME_ROLES]);
		const roles = source.slice(source.indexOf("var ColorRoles = []string{"), source.indexOf("// UnpaintedRoles are accepted"));
		expect(Array.from(roles.matchAll(/"([A-Za-z]+)"/g), (match) => match[1])).toEqual([...THEME_COLOR_ROLES]);
	});
});

describe("customThemePalette", () => {
	it("fills the swatch from the theme, and from the default look for what it leaves out", () => {
		const palette = customThemePalette(dark({ canvas: "#0b1020", accent: "#e0a84f" }), "dark");
		expect(palette.bg).toBe("#0b1020");
		expect(palette.primary).toBe("#e0a84f");
		expect(palette.green).toBe(getThemeDefinition("orchestrate").dark.green);
	});

	it("shows a one-appearance theme's own colors in the other appearance", () => {
		expect(customThemePalette(dark({ canvas: "#0b1020" }), "light").bg).toBe("#0b1020");
	});
});

describe("buildCustomThemeCss", () => {
	it("scopes each appearance to the theme, then appends the theme's CSS", () => {
		const css = buildCustomThemeCss(
			dark({ canvas: "#101012", sidebar: "#3f0e40" }, { variants: { light: { canvas: "#fafafa" } }, css: " [data-workspace-area=\"composer\"] { border-radius: 4px; } " }),
		);
		const darkRoot = ':root[data-style-theme="custom:sumi"]:not([data-theme="light"])';
		const lightRoot = ':root[data-style-theme="custom:sumi"][data-theme="light"]';
		expect(css.startsWith(`${darkRoot} {\n\tcolor-scheme: dark;`)).toBe(true);
		expect(css).toContain("\t--background: #101012;");
		expect(css).toContain(`${darkRoot} :is([data-slot="sidebar-inner"], [data-theme-frame],`);
		expect(css).toContain(`${lightRoot} {\n\tcolor-scheme: light;\n\t--background: #fafafa;`);
		// Light never names the dark sidebar.
		expect(css.slice(css.indexOf(lightRoot))).not.toContain("#3f0e40");
		expect(css.endsWith('[data-workspace-area="composer"] { border-radius: 4px; }')).toBe(true);
	});

	it("writes nothing for an appearance the theme has no colors for", () => {
		const css = buildCustomThemeCss(dark({ accent: "#e0a84f" }));
		expect(css).not.toContain('[data-theme="light"] {');
		expect(css).not.toContain("color-scheme: light");
	});

	it("adds row, bubble, code and placeholder rules only when the theme sets them", () => {
		const plain = buildCustomThemeCss(dark({ canvas: "#101012" }));
		for (const absent of [":hover", ".cursor-chat-human-message", ".chat-code", "::placeholder"]) expect(plain).not.toContain(absent);

		const full = buildCustomThemeCss(dark({ sidebarRowSelected: "#1164a3", messageSurface: "#1164a3", codeBackground: "#000000", placeholder: "#777777" }));
		expect(full).toContain('[data-slot="sidebar-inner"] :hover {');
		// The selected rule comes after hover, so a selected row keeps its fill under the pointer.
		expect(full.indexOf('[data-active="true"] {\n\t--sidebar-accent: #1164a3;')).toBeGreaterThan(full.indexOf(":hover {"));
		expect(full).toContain(".cursor-chat-human-message.bg-raised {\n\tbackground-color: #1164a3;");
		expect(full).toContain(".chat-code {\n\tbackground-color: #000000;");
		expect(full).toContain("::placeholder {\n\tcolor: #777777;");
	});
});

describe("themeCssValueFetches", () => {
	it("allows embedded files, fragment references and plain values", () => {
		expect(themeCssValueFetches('url("data:font/woff2;base64,AAAA") format("woff2")')).toBe(false);
		expect(themeCssValueFetches("url(#gradient)")).toBe(false);
		expect(themeCssValueFetches("#3f3f46")).toBe(false);
		expect(themeCssValueFetches('"Inter", sans-serif')).toBe(false);
	});

	it("refuses anything that would load a file", () => {
		expect(themeCssValueFetches('url("https://evil.example/a.png")')).toBe(true);
		expect(themeCssValueFetches("url(a.png)")).toBe(true);
		expect(themeCssValueFetches('url( "data:x" ), url(//evil.example/b)')).toBe(true);
		expect(themeCssValueFetches('image-set("data:x" 1x)')).toBe(true);
		expect(themeCssValueFetches('-webkit-image-set("data:x" 1x)')).toBe(true);
		expect(themeCssValueFetches('src("https://evil.example/a.png")')).toBe(true);
		expect(themeCssValueFetches("u\\72l(https://evil.example/a.png)")).toBe(true);
	});
});

describe("stripThemeDeclarations", () => {
	const block = (declarations: Record<string, string>, cssText: string) => {
		const removed: string[] = [];
		const names = Object.keys(declarations);
		const style = {
			length: names.length,
			item: (index: number) => names[index] ?? "",
			getPropertyValue: (name: string) => declarations[name] ?? "",
			removeProperty: (name: string) => {
				removed.push(name);
				return "";
			},
			cssText,
		};
		return { style, removed };
	};

	it("removes a declaration that loads a file and keeps the rest", () => {
		const { style, removed } = block({ "background-image": 'url("https://evil.example/a.png")', color: "red" }, "color: red;");
		stripThemeDeclarations(style);
		expect(removed).toEqual(["background-image"]);
		expect(style.cssText).toBe("color: red;");
	});

	it("drops the whole block when an unparsed shorthand hides the load behind a var()", () => {
		// The browser keeps `background: url(…) var(--x)` unparsed: its longhands
		// read back empty, and only the block's text shows the url.
		const { style, removed } = block({ "background-image": "", "background-color": "" }, 'background: url("https://evil.example/a.png") var(--x);');
		stripThemeDeclarations(style);
		expect(removed).toEqual([]);
		expect(style.cssText).toBe("");
	});

	it("removes longhands the browser will not show, where an overridden shorthand hides its url", () => {
		// `background: url(https://…) var(--x); background-color: red` as Chromium
		// reports it: the shorthand is gone from the text and its longhands are
		// empty, yet the url still loads when the rule applies.
		const { style, removed } = block(
			{ "background-image": "", "background-size": "", "background-color": "red" },
			"background-image: ; background-size: ; background-color: red;",
		);
		stripThemeDeclarations(style);
		expect(removed.sort()).toEqual(["background-image", "background-size"]);
	});

	it("keeps a var() shorthand the block still shows", () => {
		// `background: var(--img) center / cover`: unparsed too, but readable.
		const { style, removed } = block({ "background-image": "", "background-size": "" }, "background: var(--img) center / cover;");
		stripThemeDeclarations(style);
		expect(removed).toEqual([]);
		expect(style.cssText).toBe("background: var(--img) center / cover;");
	});

	it("keeps a block whose only url is an embedded file", () => {
		const text = 'background: url("data:image/png;base64,AAAA") var(--x);';
		const { style } = block({ "background-image": "" }, text);
		stripThemeDeclarations(style);
		expect(style.cssText).toBe(text);
	});
});

describe("themeAppearanceSelector", () => {
	it("points .dark appearance selectors at this app's marker", () => {
		expect(themeAppearanceSelector('.dark [data-workspace-area="conversation"]::before')).toBe(
			':root:not([data-theme="light"]) [data-workspace-area="conversation"]::before',
		);
		expect(themeAppearanceSelector(":root:not(.dark) .chat-markdown")).toBe(':root[data-theme="light"] .chat-markdown');
	});

	it("leaves other selectors alone, including classes that only start with dark", () => {
		for (const selector of ['[data-slot="composer-host"]', ".darker", ".dark-mode a", '[data-theme="dark"] a']) {
			expect(themeAppearanceSelector(selector)).toBe(selector);
		}
	});
});

describe("isCustomThemeStyle", () => {
	it("accepts custom ids only", () => {
		expect(isCustomThemeStyle(customThemeStyle("sumi"))).toBe(true);
		expect(isCustomThemeStyle("nord")).toBe(false);
		expect(isCustomThemeStyle("custom:Not Ok")).toBe(false);
	});
});

describe("takeCustomThemeSwitch", () => {
	beforeEach(() => window.localStorage.removeItem(customThemeSwitchStorageKey));

	it("applies an agent's request once", () => {
		const request = { themeId: "sumi", mode: "dark", id: "req-1" };
		expect(takeCustomThemeSwitch(request)).toEqual({ themeId: "sumi", mode: "dark" });
		expect(takeCustomThemeSwitch(request)).toBeNull();
		expect(takeCustomThemeSwitch({ ...request, id: "req-2" })).toEqual({ themeId: "sumi", mode: "dark" });
	});

	it("ignores unknown modes and requests without an id", () => {
		expect(takeCustomThemeSwitch({ themeId: "", mode: "dim", id: "req-3" })).toEqual({});
		expect(takeCustomThemeSwitch({ themeId: "nord", mode: "dark", id: "" })).toBeNull();
	});
});
