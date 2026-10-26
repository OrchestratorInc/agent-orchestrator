import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const announce = vi.hoisted(() => vi.fn());
const theme = vi.hoisted(() => ({
	accent: "#accent",
	amber: "#amber",
	bgElevated: "#elevated",
	bgSurface: "#surface",
	borderSubtle: "#border",
	fontMono: "GeistMono_400Regular",
	green: "#green",
	red: "#red",
	textPrimary: "#primary",
	textSecondary: "#secondary",
	textTertiary: "#tertiary",
	tintRed: "#tintRed",
}));

vi.mock("react-native", () => ({
	AccessibilityInfo: { announceForAccessibility: announce },
	ScrollView: "ScrollView",
	StyleSheet: { create: (styles: object) => styles },
	Text: "Text",
	View: "View",
}));
vi.mock("./ThemeProvider", () => ({
	useTheme: () => theme,
	useThemedStyles: (make: (t: typeof theme) => object) => make(theme),
	useThemeState: () => ({ scheme: "dark" }),
}));
vi.mock("./ui", () => ({ SheetHeader: "SheetHeader", SHEET_SCROLL_CONTENT: { padding: 20 } }));
vi.mock("./divider", () => ({ rowDividerWidth: 1 }));
vi.mock("./AppSymbol", () => ({ AppSymbol: "AppSymbol" }));
vi.mock("./item-actions-menu", () => ({ ItemActionsMenu: "ItemActionsMenu" }));
vi.mock("./item-actions-menu.ios", () => ({ ItemActionsPullDown: "ItemActionsPullDown" }));
vi.mock("./reviewer-picker", () => ({ ReviewerPicker: "ReviewerPicker" }));
vi.mock("./reviewer-picker.ios", () => ({ ReviewerPicker: "ReviewerPicker" }));
vi.mock("./review-drawables", () => ({ reviewDrawable: (name: string) => `drawable:${name}` }));
vi.mock("@expo/ui", async () => {
	const React = await import("react");
	return { Host: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("Host", props, children) };
});
vi.mock("@expo/ui/jetpack-compose", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		CircularProgressIndicator: host("CircularProgressIndicator"),
		Column: host("Column"),
		Host: host("ComposeHost"),
		Icon: host("Icon"),
		ListItem: Object.assign(host("ListItem"), {
			HeadlineContent: host("Headline"),
			SupportingContent: host("Supporting"),
			LeadingContent: host("Leading"),
			TrailingContent: host("Trailing"),
		}),
		Switch: host("ComposeSwitch"),
		Text: host("ComposeText"),
	};
});
vi.mock("@expo/ui/jetpack-compose/modifiers", () => ({
	alpha: (value: number) => ({ alpha: value }),
	clickable: (handler: () => void) => ({ clickable: handler }),
	size: (width: number, height: number) => ({ size: [width, height] }),
	testID: (tag: string) => ({ testID: tag }),
	toggleable: (value: boolean, handler: () => void, options: object) => ({ toggleable: { value, handler, ...options } }),
}));
vi.mock("@expo/ui/swift-ui", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		Button: host("SwiftButton"),
		Form: host("Form"),
		HStack: host("HStack"),
		Image: host("Image"),
		Label: host("Label"),
		ProgressView: host("ProgressView"),
		Section: host("Section"),
		Spacer: host("Spacer"),
		Text: host("SwiftText"),
		Toggle: host("Toggle"),
		VStack: host("VStack"),
	};
});
vi.mock("@expo/ui/swift-ui/modifiers", () => {
	const record = (name: string) => (...args: unknown[]) => ({ modifier: name, args });
	return {
		accessibilityHint: record("accessibilityHint"),
		disabled: record("disabled"),
		font: record("font"),
		foregroundStyle: record("foregroundStyle"),
		italic: record("italic"),
		lineLimit: record("lineLimit"),
		listRowBackground: record("listRowBackground"),
		scrollContentBackground: record("scrollContentBackground"),
		textSelection: record("textSelection"),
		tint: record("tint"),
	};
});

import { ReviewActionsSheet } from "./review-actions-sheet";
import { ReviewActionsSheet as IOSReviewActionsSheet } from "./review-actions-sheet.ios";
import type { ReviewActionsSheetProps } from "./review-actions-sheet.types";
import { stateOpacity } from "./tokens";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	announce.mockClear();
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
});

afterEach(async () => {
	if (renderer) await act(async () => renderer?.unmount());
	renderer = undefined;
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

async function render(element: React.ReactElement) {
	await act(async () => { renderer = create(element); });
	return renderer!.root;
}

function sheet(overrides: Partial<ReviewActionsSheetProps> = {}): ReviewActionsSheetProps {
	return {
		subtitle: "PR #12 · Fix login",
		alerts: [],
		policies: [
			{ key: "autoReviewEnabled", title: "Auto review", description: "Reviews every push.", value: false, busy: false },
			{ key: "autoInjectReview", title: "Automatically fix review comments", description: "Sends review comments to the worker.", value: true, busy: true },
		],
		locked: false,
		onPolicyChange: vi.fn(),
		externalReviewers: [{ id: "octocat", detail: "2 comments", busy: false }, { id: "hubot", detail: "1 comment", busy: true }],
		onRerequest: vi.fn(),
		feedback: [{
			id: "unresolved",
			title: "Unresolved comments",
			footer: "Use ⋯ to open the exact GitHub file and line.",
			items: [{ key: "c1", author: "octocat", meta: "GitHub · open", location: "src/a.ts:4", body: "Rename this.", bodyMissing: false, notInjected: true, sent: true, menuLabel: "Actions for octocat's comment", actions: [], working: false }],
		}],
		...overrides,
	};
}

const sectionTitles = (root: ReactTestRenderer["root"], type: string, read: (node: ReactTestRenderer["root"]) => unknown) => root.findAllByType(type as never).map(read);

describe("review actions sheet on Android", () => {
	it("keeps the sheet's own scroll view so the bottom sheet's drag still works", async () => {
		const root = await render(<ReviewActionsSheet {...sheet()} />);
		expect(root.findByType("ScrollView" as never).props.nestedScrollEnabled).toBe(true);
	});

	it("draws each automation setting as a Material switch row that toggles as a whole", async () => {
		const onPolicyChange = vi.fn();
		const root = await render(<ReviewActionsSheet {...sheet({ onPolicyChange })} />);
		const [autoReview, saving] = root.findAllByType("ListItem" as never).slice(0, 2);
		const toggle = autoReview.props.modifiers.find((entry: { toggleable?: object }) => entry.toggleable).toggleable;
		expect(toggle).toMatchObject({ value: false, role: "switch" });
		toggle.handler();
		expect(onPolicyChange).toHaveBeenCalledWith("autoReviewEnabled", true);
		autoReview.findByType("ComposeSwitch" as never).props.onCheckedChange(true);
		expect(onPolicyChange).toHaveBeenLastCalledWith("autoReviewEnabled", true);
		// Saving: a spinner holds the switch's place and the row takes no toggle.
		expect(saving.findAllByType("ComposeSwitch" as never)).toHaveLength(0);
		expect(saving.findAllByType("CircularProgressIndicator" as never)).toHaveLength(1);
		expect(saving.props.modifiers.some((entry: { toggleable?: object }) => entry.toggleable)).toBe(false);
	});

	it("asks a reviewer again from a row that ends in a spinner while it runs", async () => {
		const onRerequest = vi.fn();
		const root = await render(<ReviewActionsSheet {...sheet({ onRerequest })} />);
		const rows = root.findAllByType("ListItem" as never).slice(2);
		expect(rows[0].findAllByType("Trailing" as never)).toHaveLength(0);
		expect(rows[1].findByType("Trailing" as never).findAllByType("CircularProgressIndicator" as never)).toHaveLength(1);
		rows[0].props.modifiers.find((entry: { clickable?: () => void }) => entry.clickable).clickable();
		expect(onRerequest).toHaveBeenCalledWith("octocat");
		expect(rows[0].props.modifiers.some((entry: { alpha?: number }) => entry.alpha !== undefined)).toBe(false);
	});

	it("dims the reviewer rows that wait while another action runs", async () => {
		const root = await render(<ReviewActionsSheet {...sheet({ locked: true })} />);
		const [waiting, running] = root.findAllByType("ListItem" as never).slice(2);
		expect(waiting.props.modifiers).toEqual([{ alpha: stateOpacity.disabled }]);
		// The running row keeps full strength so its spinner reads, and takes no second press.
		expect(running.props.modifiers).toEqual([]);
	});

	it("titles sections in sentence case as headers, with the hint underneath", async () => {
		const root = await render(<ReviewActionsSheet {...sheet()} />);
		const headers = root.findAll((node) => node.type === ("Text" as never) && node.props.accessibilityRole === "header");
		expect(headers.map((node) => node.props.children)).toEqual(["Automation", "Orchestrator reviewer", "External reviewers", "Unresolved comments"]);
	});

	it("says when no reviewer can run, and hosts the picker when one can", async () => {
		let root = await render(<ReviewActionsSheet {...sheet()} />);
		expect(root.findAllByType("ReviewerPicker" as never)).toHaveLength(0);
		await act(async () => renderer?.unmount());
		root = await render(<ReviewActionsSheet {...sheet({ reviewer: { reviewers: [], selectedReviewer: "", effectiveReviewer: "", onSelectReviewer: vi.fn(), models: [], modelTitle: "Model", selectedModel: "", onSelectModel: vi.fn(), busy: false } })} />);
		expect(root.findByType("ReviewerPicker" as never).parent?.type).toBe("Column");
	});

	it("announces failures", async () => {
		const root = await render(<ReviewActionsSheet {...sheet({ alerts: ["Could not resolve this comment."] })} />);
		const alert = root.findAll((node) => node.type === ("Text" as never) && node.props.accessibilityRole === "alert");
		expect(alert.map((node) => [node.props.children, node.props.accessibilityLiveRegion])).toEqual([["Could not resolve this comment.", "polite"]]);
	});
});

describe("review actions sheet on iOS", () => {
	it("is a Form of sections with their hints as footers", async () => {
		const root = await render(<IOSReviewActionsSheet {...sheet()} />);
		expect(root.findByType("Form" as never).props.modifiers).toContainEqual({ modifier: "scrollContentBackground", args: ["hidden"] });
		const sections = sectionTitles(root, "Section", (node) => node.props.title);
		expect(sections).toEqual(["Automation", "Orchestrator reviewer", "External reviewers", "Unresolved comments"]);
		expect(root.findAllByType("Section" as never)[0].props.footer.props.children).toBe("The same settings as this session's inspector on desktop.");
	});

	it("draws an automation setting as a system toggle, and a spinner while it saves", async () => {
		const onPolicyChange = vi.fn();
		const root = await render(<IOSReviewActionsSheet {...sheet({ onPolicyChange })} />);
		const toggles = root.findAllByType("Toggle" as never);
		expect(toggles).toHaveLength(1);
		expect(toggles[0].findAllByType("SwiftText" as never).map((text) => text.props.children)).toEqual(["Auto review", "Reviews every push."]);
		// No tint: the system's own switch colours.
		expect(toggles[0].props.modifiers.some((entry: { modifier: string }) => entry.modifier === "tint")).toBe(false);
		toggles[0].props.onIsOnChange(true);
		expect(onPolicyChange).toHaveBeenCalledWith("autoReviewEnabled", true);
		expect(root.findAllByType("ProgressView" as never).length).toBeGreaterThanOrEqual(1);
	});

	it("disables the controls while another action runs", async () => {
		const root = await render(<IOSReviewActionsSheet {...sheet({ locked: true })} />);
		expect(root.findByType("Toggle" as never).props.modifiers).toContainEqual({ modifier: "disabled", args: [true] });
		for (const button of root.findAllByType("SwiftButton" as never)) expect(button.props.modifiers).toContainEqual({ modifier: "disabled", args: [true] });
	});

	it("asks a reviewer again from a button row with progress, not a disclosure", async () => {
		const onRerequest = vi.fn();
		const root = await render(<IOSReviewActionsSheet {...sheet({ onRerequest })} />);
		const [idle, busy] = root.findAllByType("SwiftButton" as never);
		expect(idle.findAllByType("ProgressView" as never)).toHaveLength(0);
		expect(busy.findAllByType("ProgressView" as never)).toHaveLength(1);
		idle.props.onPress();
		expect(onRerequest).toHaveBeenCalledWith("octocat");
	});

	it("lists feedback with its menu, its location and whether it was sent", async () => {
		const root = await render(<IOSReviewActionsSheet {...sheet()} />);
		const menu = root.findByType("ItemActionsPullDown" as never);
		expect(menu.props).toMatchObject({ accessibilityLabel: "Actions for octocat's comment", disabled: false });
		const texts = root.findAllByType("SwiftText" as never).map((text) => text.props.children);
		expect(texts).toEqual(expect.arrayContaining(["octocat", "GitHub · open", "src/a.ts:4", "Rename this.", "Not automatically sent to the worker"]));
		expect(root.findAll((node) => node.type === ("Label" as never) && node.props.title === "Sent to worker")).toHaveLength(1);
	});

	it("reads each new failure aloud once", async () => {
		await render(<IOSReviewActionsSheet {...sheet({ alerts: ["Could not load review actions."] })} />);
		expect(announce).toHaveBeenCalledWith("Could not load review actions.");
		await act(async () => renderer!.update(<IOSReviewActionsSheet {...sheet({ alerts: ["Could not load review actions.", "Pull request not found."] })} />));
		expect(announce.mock.calls.map((call) => call[0])).toEqual(["Could not load review actions.", "Pull request not found."]);
	});
});
