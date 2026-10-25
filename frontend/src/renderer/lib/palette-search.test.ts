import { describe, expect, it } from "vitest";
import {
	abbreviationScore,
	applyNamesFirst,
	buildNameIndex,
	matchName,
	NAMES_FIRST_COVERAGE,
	stemWord,
	withinOneEdit,
	withinTwoEdits,
	type NameTarget,
	type RankedEntry,
} from "./palette-search";

// A slice of the real catalog: shortcut actions, settings rows and palette
// commands with the keywords the registry gives them.
const catalog: (NameTarget & { id: string })[] = [
	{ id: "toggle-sidebar", kind: "action", title: "Toggle sidebar", keywords: ["show", "hide", "open", "close", "left panel"] },
	{ id: "toggle-terminal", kind: "action", title: "Toggle terminal", keywords: ["show", "hide", "open", "close", "bottom panel", "shell"] },
	{ id: "toggle-diff", kind: "action", title: "Toggle diff", keywords: ["show", "hide", "open", "close", "changes", "git diff"] },
	{ id: "toggle-theme", kind: "action", title: "Toggle theme", keywords: ["dark", "light", "mode", "appearance"] },
	{ id: "keyboard-shortcuts", kind: "action", title: "Show keyboard shortcuts", keywords: ["keybindings", "hotkeys", "keys"] },
	{ id: "archive-session", kind: "action", title: "Archive session", keywords: ["hide", "done", "remove"] },
	{ id: "model-picker", kind: "action", title: "Choose model", keywords: ["switch model", "llm", "claude"] },
	{ id: "theme", kind: "setting", title: "Theme", keywords: ["dark mode", "light mode", "appearance"] },
	{ id: "language", kind: "setting", title: "Language", keywords: ["locale", "translation", "chinese"] },
	{ id: "analytics", kind: "setting", title: "Share usage analytics", keywords: ["telemetry", "tracking", "opt out"] },
	// Registry order: a settings page comes before the rows that inherit its keywords.
	{ id: "mobile", kind: "setting", title: "Mobile", keywords: ["phone", "ios", "android", "pair", "qr code"] },
	{ id: "keep-awake", kind: "setting", title: "Keep this Mac awake", keywords: ["sleep", "caffeinate", "phone", "pair"] },
	{ id: "updates-channel", kind: "setting", title: "Updates channel", keywords: ["nightly", "beta", "stable"] },
	{ id: "session", kind: "session", title: "Fix flaky login test", keywords: ["ao/fix-login", "my-project"] },
];

const index = buildNameIndex(catalog);

function best(query: string): string | undefined {
	let top: { id: string; score: number } | undefined;
	for (const target of catalog) {
		const { score } = matchName(query, target, index);
		if (score > 0 && (!top || score > top.score)) top = { id: target.id, score };
	}
	return top?.id;
}

describe("matchName", () => {
	it.each([
		["toggle sidebar", "toggle-sidebar"],
		["hide sidebar", "toggle-sidebar"],
		["keybindings", "keyboard-shortcuts"],
		["telemetry", "analytics"],
		["turn off analytics", "analytics"],
		["change language to chinese", "language"],
		["switch model", "model-picker"],
		["git changes", "toggle-diff"],
		["pair phone", "mobile"],
		["nightly", "updates-channel"],
		["flaky login", "session"],
	])("%s finds %s", (query, want) => {
		expect(best(query)).toBe(want);
	});

	it.each([
		["toggle sidbar", "toggle-sidebar"],
		["langauge", "language"],
		["keyboard shrtcuts", "keyboard-shortcuts"],
		["archve sesion", "archive-session"],
		["tgl term", "toggle-terminal"],
	])("tolerates the typo or abbreviation %s", (query, want) => {
		expect(best(query)).toBe(want);
	});

	it("finds the theme for dark mode, as a setting or the toggle", () => {
		expect(["theme", "toggle-theme"]).toContain(best("dark mode"));
	});

	it("scores an exact name above a partial one", () => {
		const exact = matchName("theme", catalog.find((t) => t.id === "theme")!, index);
		const partial = matchName("theme", catalog.find((t) => t.id === "toggle-theme")!, index);
		expect(exact.score).toBeGreaterThan(partial.score);
		expect(exact.coverage).toBe(1);
	});

	it("does not match unrelated names", () => {
		expect(matchName("kubernetes", catalog[0]!, index)).toEqual({ score: 0, coverage: 0 });
		expect(matchName("   ", catalog[0]!, index).score).toBe(0);
	});
});

describe("text helpers", () => {
	it("stems the forms people type", () => {
		expect(stemWord("closing")).toBe(stemWord("close"));
		expect(stemWord("builds")).toBe(stemWord("build"));
		expect(stemWord("settings")).toBe(stemWord("setting"));
		expect(stemWord("closes")).toBe(stemWord("close"));
		expect(stemWord("processes")).toBe(stemWord("process"));
		expect(stemWord("pass")).toBe("pass");
	});

	it("bounds edit distance", () => {
		expect(withinOneEdit("sidbar", "sidebar")).toBe(true);
		expect(withinOneEdit("langauge", "language")).toBe(true);
		expect(withinOneEdit("sidebar", "sidebars")).toBe(true);
		expect(withinOneEdit("sidebar", "slidebars")).toBe(false);
		expect(withinTwoEdits("configuraton", "configuration")).toBe(true);
		expect(withinTwoEdits("configuration", "xxnfiguraxxon")).toBe(false);
	});

	it("places abbreviation letters in order or not at all", () => {
		expect(abbreviationScore("tglterm", "toggleterminal")).not.toBeNull();
		expect(abbreviationScore("zz", "toggleterminal")).toBeNull();
	});
});

describe("applyNamesFirst", () => {
	const entries = (nameCoverage: number, decisive: boolean): RankedEntry<string>[] => [
		{ item: "theme", score: 4, coverage: nameCoverage },
		{ item: "file mentioning dark mode", score: 7, decisive },
	];

	it("keeps a clearly named control above content that merely mentions it", () => {
		const ranked = applyNamesFirst(entries(NAMES_FIRST_COVERAGE, false));
		const file = ranked.find((e) => e.item === "file mentioning dark mode")!;
		expect(file.score).toBeLessThan(4);
	});

	it("lets decisive content win", () => {
		const ranked = applyNamesFirst(entries(1, true));
		expect(ranked.find((e) => e.item === "file mentioning dark mode")!.score).toBe(7);
	});

	it("leaves content alone when no name covers the query", () => {
		const ranked = applyNamesFirst(entries(0.3, false));
		expect(ranked.find((e) => e.item === "file mentioning dark mode")!.score).toBe(7);
	});
});
