import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	theme: new Proxy({}, { get: (_target, key) => `#${String(key)}` }),
	select: vi.fn(),
	setOptions: vi.fn(),
	os: "ios",
}));

vi.mock("expo-router", () => ({ useNavigation: () => ({ setOptions: fixtures.setOptions }) }));
vi.mock("./ThemeProvider", () => ({ useTheme: () => fixtures.theme, useThemedStyles: (make: (theme: object) => object) => make(fixtures.theme) }));
vi.mock("./haptics", () => ({ haptics: { select: fixtures.select } }));
vi.mock("./icons", async () => {
	const React = await import("react");
	return { Feather: (props: object) => React.createElement("Feather", props) };
});
vi.mock("react-native-svg", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return { default: host("Svg"), Defs: host("Defs"), Ellipse: host("Ellipse"), RadialGradient: host("RadialGradient"), Stop: host("Stop") };
});
vi.mock("react-native-reanimated", async () => {
	const React = await import("react");
	const fade = { duration: () => fade };
	return { default: { View: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("AnimatedView", props, children) }, FadeIn: fade, FadeOut: fade };
});
vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		Platform: { get OS() { return fixtures.os; } },
		StyleSheet: { create: (styles: object) => styles, absoluteFill: {} },
		Text: host("Text"),
		View: host("View"),
		ScrollView: host("ScrollView"),
		Pressable: ({ children, style, ...props }: { children?: React.ReactNode; style?: unknown }) =>
			React.createElement("Pressable", { ...props, style: typeof style === "function" ? style({ pressed: false }) : style }, children),
	};
});

import type { DashboardPR } from "./api";
import { PullRequestPicker, PullRequestPickerOverlay, PR_PICKER_ROW_HEIGHT } from "./pull-request-picker";
import type { PullRequestRow } from "./sessionPullRequests";

const row = (number: number, title: string, status: string, tone: PullRequestRow["status"]["tone"], glyphTone: PullRequestRow["glyphTone"] = "success"): PullRequestRow => ({
	pr: { number, url: `https://forge.test/pr/${number}`, state: "open" } as DashboardPR,
	number,
	title,
	lifecycle: "open",
	glyph: "git-pull-request",
	glyphTone,
	status: { text: status, tone },
	accessibilityLabel: `Pull request ${number}, ${title}, ${status}`,
});
const FIVE = [
	row(9137, "Create brand doc", "Mergeable", "success"),
	row(9140, "Archive paging", "Draft", "passive", "passive"),
	row(9125, "Topo background", "Merged", "merged", "merged"),
	row(9120, "Pairing code hint", "Merged", "merged", "merged"),
	row(9001, "Dictation keeps the screen awake", "Closed", "passive", "error"),
];

let renderer: ReactTestRenderer | undefined;
beforeEach(() => { vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true); fixtures.select.mockClear(); fixtures.setOptions.mockClear(); });
afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; vi.unstubAllGlobals(); });

function render(rows: PullRequestRow[], onChoose = vi.fn()) {
	act(() => { renderer = create(<PullRequestPicker rows={rows} onChoose={onChoose} />); });
	return { onChoose, root: renderer!.root };
}
const flat = (style: unknown): Record<string, unknown> => (Array.isArray(style) ? Object.assign({}, ...style.flat(Infinity).filter(Boolean).map(flat)) : (style as Record<string, unknown>) ?? {});
const texts = (node: ReactTestInstance) => node.findAll((n) => (n.type as unknown) === "Text");

describe("PullRequestPicker", () => {
	it("lists every row in order with number, title and status in their tones", () => {
		const { root } = render(FIVE);
		const rows = root.findAll((n) => (n.type as unknown) === "Pressable");
		expect(rows.map((r) => r.props.testID)).toEqual(FIVE.map((r) => `pull-request-${r.number}`));
		const first = texts(rows[0]).map((t) => t.props.children).flat(Infinity).join("");
		expect(first).toContain("#9137");
		expect(first).toContain("Create brand doc");
		expect(first).toContain("Mergeable");
		const status = texts(rows[0]).find((t) => t.props.children === "Mergeable")!;
		expect(flat(status.props.style).color).toBe("#green");
		expect(rows[4].findByType("Feather" as never).props.color).toBe("#red");
	});

	it("picks a row", () => {
		const { root, onChoose } = render(FIVE);
		act(() => root.findByProps({ testID: "pull-request-9125" }).props.onPress());
		expect(onChoose).toHaveBeenCalledWith(FIVE[2].pr);
		expect(fixtures.select).toHaveBeenCalledOnce();
	});

	// Review Focus 5: long titles and large text stay one line each.
	it("keeps every text on one line", () => {
		const { root } = render(FIVE);
		// A span inside another Text takes its parent's truncation; check the outer lines.
		const insideText = (node: ReactTestInstance) => { for (let p = node.parent; p; p = p.parent) if ((p.type as unknown) === "Text") return true; return false; };
		const all = root.findAll((n) => (n.type as unknown) === "Pressable").flatMap(texts).filter((t) => !insideText(t));
		expect(all).toHaveLength(FIVE.length * 2);
		for (const text of all) expect(text.props.numberOfLines).toBe(1);
	});

	it("scrolls past five rows, inside five and a half", () => {
		const seven = [...FIVE, row(8990, "Seventh", "Merged", "merged"), row(8980, "Eighth", "Merged", "merged")];
		const { root } = render(seven);
		const scroll = root.findByType("ScrollView" as never);
		expect(flat(scroll.props.style).maxHeight).toBe(PR_PICKER_ROW_HEIGHT * 5.5);
		expect(scroll.props.scrollEnabled).toBe(true);
		act(() => renderer!.update(<PullRequestPicker rows={FIVE.slice(0, 3)} onChoose={vi.fn()} />));
		expect(renderer!.root.findByType("ScrollView" as never).props.scrollEnabled).toBe(false);
	});

	it("is a list of buttons, not a radio group", () => {
		const { root } = render(FIVE);
		expect(root.findAll((n) => n.props.accessibilityRole === "radio" || n.props.accessibilityRole === "radiogroup")).toHaveLength(0);
		const first = root.findByProps({ testID: "pull-request-9137" });
		expect(first.props).toMatchObject({ accessibilityRole: "button", accessibilityLabel: FIVE[0].accessibilityLabel, accessibilityHint: "Opens the pull request review" });
	});

	it("rows are 58 points tall", () => {
		expect(PR_PICKER_ROW_HEIGHT).toBe(58);
	});
});

describe("PullRequestPickerOverlay", () => {
	it("floats above the composer, marks its touches, and turns the iOS back swipe off while up", () => {
		act(() => { renderer = create(<PullRequestPickerOverlay bottom={74} rows={FIVE} onChoose={vi.fn()} />); });
		const overlay = renderer!.root.findByType("AnimatedView" as never);
		expect(flat(overlay.props.style).bottom).toBe(74);
		expect(typeof overlay.props.onTouchStart).toBe("function");
		expect(fixtures.setOptions).toHaveBeenCalledWith({ gestureEnabled: false });
		expect(renderer!.root.findAllByType("Svg" as never)).toHaveLength(1);
		act(() => renderer!.unmount());
		renderer = undefined;
		expect(fixtures.setOptions).toHaveBeenLastCalledWith({ gestureEnabled: true });
	});
});
