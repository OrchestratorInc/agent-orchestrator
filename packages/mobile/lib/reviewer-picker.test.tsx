import { useState } from "react";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const pickerMounts = vi.hoisted(() => ({ count: 0 }));

vi.mock("./ThemeProvider", () => ({
	useTheme: () => ({ bgElevated: "#elevated", bgSurface: "#surface", textPrimary: "#primary", textSecondary: "#secondary", textTertiary: "#tertiary" }),
}));
vi.mock("./toolbarIcon.android", () => ({ materialDrawable: (name: string) => `drawable:${name}` }));
vi.mock("./harnessLogoAssets", () => ({ logoFor: (harness: string) => (harness === "codex" ? "logo:codex" : harness === "opencode" ? "logo:opencode" : null) }));
vi.mock("./harnessLogo", () => ({ chipColorFor: (harness: string) => (harness === "opencode" ? "#chip" : undefined) }));
vi.mock("./spawn-composer-controls.ios", () => ({ HarnessImage: "HarnessImage", useHarnessLogoUris: () => ({ codex: "file://codex.png" }) }));
vi.mock("@expo/ui/jetpack-compose", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		Box: host("Box"),
		DropdownMenu: Object.assign(host("DropdownMenu"), { Trigger: host("MenuTrigger"), Items: host("MenuItems") }),
		DropdownMenuItem: Object.assign(host("DropdownMenuItem"), { Text: host("ItemText"), LeadingIcon: host("ItemLeadingIcon"), TrailingIcon: host("ItemTrailingIcon") }),
		Icon: host("Icon"),
		ListItem: Object.assign(host("ListItem"), {
			HeadlineContent: host("Headline"),
			SupportingContent: host("Supporting"),
			LeadingContent: host("Leading"),
			TrailingContent: host("Trailing"),
		}),
		Text: host("ComposeText"),
	};
});
vi.mock("@expo/ui/jetpack-compose/modifiers", () => ({
	alpha: (value: number) => ({ alpha: value }),
	background: (color: string) => ({ background: color }),
	clickable: (handler: () => void) => ({ clickable: handler }),
	clip: (shape: unknown) => ({ clip: shape }),
	fillMaxWidth: () => ({ fillMaxWidth: true }),
	Shapes: { RoundedCorner: (value: number) => ({ roundedCorner: value }) },
	size: (width: number, height: number) => ({ size: [width, height] }),
	testID: (tag: string) => ({ testID: tag }),
}));
vi.mock("@expo/ui/swift-ui", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	// Counts mounts, so a remount onto the sheet's value can be seen.
	function Picker({ children, ...props }: { children?: React.ReactNode }) {
		React.useEffect(() => { pickerMounts.count += 1; }, []);
		return React.createElement("Picker", props, children);
	}
	return { Label: host("Label"), Picker, Text: host("SwiftText") };
});
vi.mock("@expo/ui/swift-ui/modifiers", () => {
	const record = (name: string) => (...args: unknown[]) => ({ modifier: name, args });
	return {
		accessibilityIdentifier: record("accessibilityIdentifier"),
		disabled: record("disabled"),
		font: record("font"),
		listRowBackground: record("listRowBackground"),
		pickerStyle: record("pickerStyle"),
		tag: record("tag"),
		tint: record("tint"),
	};
});

import { ReviewerPicker } from "./reviewer-picker";
import { ReviewerPicker as IOSReviewerPicker } from "./reviewer-picker.ios";
import type { ReviewerPickerProps } from "./reviewer-picker.types";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	pickerMounts.count = 0;
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

function props(overrides: Partial<ReviewerPickerProps> = {}): ReviewerPickerProps {
	return {
		reviewers: [{ id: "codex", label: "Codex" }, { id: "opencode", label: "OpenCode" }],
		selectedReviewer: "",
		effectiveReviewer: "codex",
		onSelectReviewer: vi.fn(),
		models: [{ id: "gpt-5", label: "GPT-5" }],
		modelTitle: "Model",
		selectedModel: "",
		onSelectModel: vi.fn(),
		busy: false,
		...overrides,
	};
}

async function render(element: React.ReactElement) {
	await act(async () => { renderer = create(element); });
	return renderer!.root;
}

const texts = (node: { findAllByType(type: never): { props: { children?: unknown } }[] }) => node.findAllByType("ComposeText" as never).map((text) => text.props.children);

describe("ReviewerPicker on Android", () => {
	it("is a list item per setting that reads its current value", async () => {
		const root = await render(<ReviewerPicker {...props()} />);
		const rows = root.findAllByType("ListItem" as never);
		expect(rows).toHaveLength(2);
		expect(texts(rows[0].findByType("Headline" as never))).toEqual(["Reviewer"]);
		expect(texts(rows[0].findByType("Supporting" as never))).toEqual(["Project default · Codex"]);
		expect(rows[0].findByType("Leading" as never).findByType("Icon" as never).props).toMatchObject({ source: "logo:codex", tint: null });
		expect(texts(rows[1].findByType("Supporting" as never))).toEqual(["Provider default"]);
		expect(rows[0].props.modifiers).toContainEqual({ testID: "review-reviewer" });
		expect(rows[1].props.modifiers).toContainEqual({ testID: "review-model" });
	});

	it("opens a dropdown of the choices with a check on the current one, and closes on a choice", async () => {
		const onSelectReviewer = vi.fn();
		const root = await render(<ReviewerPicker {...props({ selectedReviewer: "opencode", onSelectReviewer })} />);
		const reviewerMenu = () => root.findAllByType("DropdownMenu" as never)[0];
		expect(reviewerMenu().props.expanded).toBe(false);
		const open = root.findAllByType("ListItem" as never)[0].props.modifiers.find((entry: { clickable?: () => void }) => entry.clickable).clickable;
		act(() => open());
		expect(reviewerMenu().props.expanded).toBe(true);
		const choices = reviewerMenu().findByType("MenuItems" as never).findAllByType("DropdownMenuItem" as never);
		expect(choices.map((choice) => texts(choice.findByType("ItemText" as never))[0])).toEqual(["Project default · Codex", "Codex", "OpenCode"]);
		expect(choices.map((choice) => choice.findAllByType("ItemTrailingIcon" as never).length)).toEqual([0, 0, 1]);
		expect(choices[2].findByType("ItemTrailingIcon" as never).findByType("Icon" as never).props.contentDescription).toBe("Selected");
		// A mark that needs a backdrop gets its chip.
		expect(choices[2].findByType("Box" as never).props.modifiers).toContainEqual({ background: "#chip" });
		act(() => choices[1].props.onClick());
		expect(onSelectReviewer).toHaveBeenCalledWith("codex");
		expect(reviewerMenu().props.expanded).toBe(false);
	});

	it("waits while the sheet is busy", async () => {
		const root = await render(<ReviewerPicker {...props({ busy: true })} />);
		const row = root.findAllByType("ListItem" as never)[0];
		expect(row.props.modifiers.some((entry: { clickable?: unknown }) => entry.clickable)).toBe(false);
		expect(row.props.modifiers).toContainEqual({ alpha: 0.4 });
	});

	it("leaves the model row out when the reviewer has no catalog", async () => {
		const root = await render(<ReviewerPicker {...props({ models: [] })} />);
		expect(root.findAllByType("ListItem" as never)).toHaveLength(1);
	});
});

describe("ReviewerPicker on iOS", () => {
	it("is a menu picker per setting, tagged by id, for the sheet's Form", async () => {
		const onSelectModel = vi.fn();
		const root = await render(<IOSReviewerPicker {...props({ selectedModel: "gpt-4", onSelectModel })} />);
		const [reviewer, model] = root.findAllByType("Picker" as never);
		expect(reviewer.props).toMatchObject({ label: "Reviewer", selection: "" });
		expect(reviewer.props.modifiers).toContainEqual({ modifier: "pickerStyle", args: ["menu"] });
		expect(reviewer.props.modifiers).toContainEqual({ modifier: "accessibilityIdentifier", args: ["review-reviewer"] });
		const options = reviewer.findAllByType("Label" as never);
		expect(options.map((option) => [option.props.title, option.props.modifiers[0].args[0]])).toEqual([["Project default · Codex", ""], ["Codex", "codex"], ["OpenCode", "opencode"]]);
		expect(options[1].props.icon.props).toMatchObject({ uri: "file://codex.png", harness: "codex" });
		// A configured model the catalog no longer lists still reads as itself.
		expect(model.findAllByType("SwiftText" as never).map((option) => option.props.children)).toEqual(["Provider default", "GPT-5", "gpt-4"]);
		act(() => model.props.onSelectionChange("gpt-5"));
		expect(onSelectModel).toHaveBeenCalledWith("gpt-5");
	});

	// Stands in for the sheet: a choice makes it busy at once (it asks before
	// switching), and later it applies the choice or keeps its own value.
	function SheetWithPicker({ outcome }: { outcome: { settle?: (apply: boolean) => void } }) {
		const [busy, setBusy] = useState(false);
		const [selected, setSelected] = useState("");
		const [pending, setPending] = useState("");
		outcome.settle = (apply) => { if (apply) setSelected(pending); setBusy(false); };
		return <IOSReviewerPicker {...props({ selectedReviewer: selected, busy, onSelectReviewer: (id) => { setPending(id); setBusy(true); } })} />;
	}

	it("goes back to the sheet's value when a choice is declined", async () => {
		const outcome: { settle?: (apply: boolean) => void } = {};
		await render(<SheetWithPicker outcome={outcome} />);
		const mounted = pickerMounts.count;
		act(() => renderer!.root.findAllByType("Picker" as never)[0].props.onSelectionChange("opencode"));
		expect(pickerMounts.count).toBe(mounted);
		await act(async () => outcome.settle!(false));
		expect(pickerMounts.count).toBeGreaterThan(mounted);
		expect(renderer!.root.findAllByType("Picker" as never)[0].props.selection).toBe("");
	});

	it("keeps the picker when the choice is applied", async () => {
		const outcome: { settle?: (apply: boolean) => void } = {};
		await render(<SheetWithPicker outcome={outcome} />);
		const mounted = pickerMounts.count;
		act(() => renderer!.root.findAllByType("Picker" as never)[0].props.onSelectionChange("opencode"));
		await act(async () => outcome.settle!(true));
		expect(pickerMounts.count).toBe(mounted);
		expect(renderer!.root.findAllByType("Picker" as never)[0].props.selection).toBe("opencode");
	});
});
