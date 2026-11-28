import { useState } from "react";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const pickerMounts = vi.hoisted(() => ({ count: 0 }));

const theme = vi.hoisted(() => ({ bgElevated: "#elevated", bgSurface: "#surface", textPrimary: "#primary", textSecondary: "#secondary", textTertiary: "#tertiary" }));
vi.mock("./ThemeProvider", () => ({
	useTheme: () => theme,
	useThemeState: () => ({ theme, scheme: "light" }),
}));
vi.mock("./toolbarIcon.android", () => ({ materialDrawable: (name: string) => `drawable:${name}` }));
vi.mock("./harnessLogoAssets", () => ({ logoFor: (harness: string) => (harness === "codex" ? "logo:codex" : harness === "opencode" ? "logo:opencode" : null) }));
vi.mock("./spawn-composer-controls.ios", () => ({ HarnessImage: "HarnessImage" }));
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
		model: { title: "Model", value: "gpt-5", valueLabel: "GPT-5", options: [{ id: "gpt-5", label: "GPT-5" }, { id: "gpt-5-mini", label: "GPT-5 mini" }] },
		onSelectModel: vi.fn(),
		effort: { title: "Effort", value: "high", valueLabel: "High", options: [{ id: "low", label: "Low" }, { id: "high", label: "High" }] },
		onSelectEffort: vi.fn(),
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
	it("reads each setting's current value, leaving out a setting the reviewer has none of", async () => {
		let root = await render(<ReviewerPicker {...props()} />);
		const rows = root.findAllByType("ListItem" as never);
		expect(rows.map((row) => texts(row.findByType("Headline" as never)))).toEqual([["Reviewer"], ["Model"], ["Effort"]]);
		expect(rows.map((row) => texts(row.findByType("Supporting" as never)))).toEqual([["Project default · Codex"], ["GPT-5"], ["High"]]);
		await act(async () => renderer?.unmount());
		root = await render(<ReviewerPicker {...props({ model: undefined, effort: undefined })} />);
		expect(root.findAllByType("ListItem" as never)).toHaveLength(1);
	});

	it("checks the effort the review runs with and saves a picked one by id", async () => {
		const onSelectEffort = vi.fn();
		const root = await render(<ReviewerPicker {...props({ onSelectEffort })} />);
		const effortMenu = () => root.findAllByType("DropdownMenu" as never)[2];
		act(() => root.findAllByType("ListItem" as never)[2].props.modifiers.find((entry: { clickable?: () => void }) => entry.clickable).clickable());
		expect(effortMenu().props.expanded).toBe(true);
		const choices = effortMenu().findAllByType("DropdownMenuItem" as never);
		expect(choices.map((choice) => texts(choice.findByType("ItemText" as never))[0])).toEqual(["Low", "High"]);
		expect(choices.map((choice) => choice.findAllByType("ItemTrailingIcon" as never).length)).toEqual([0, 1]);
		act(() => choices[0].props.onClick());
		expect(onSelectEffort).toHaveBeenCalledWith("low");
		expect(effortMenu().props.expanded).toBe(false);
	});

	it("opens a dropdown of the choices with the current one marked, and closes on a choice", async () => {
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
		act(() => choices[1].props.onClick());
		expect(onSelectReviewer).toHaveBeenCalledWith("codex");
		expect(reviewerMenu().props.expanded).toBe(false);
	});

	it("takes no press while the sheet is busy", async () => {
		const root = await render(<ReviewerPicker {...props({ busy: true })} />);
		const row = root.findAllByType("ListItem" as never)[0];
		expect(row.props.modifiers.some((entry: { clickable?: unknown }) => entry.clickable)).toBe(false);
	});
});

describe("ReviewerPicker on iOS", () => {
	it("offers each setting's choices by id, checking the value the review runs with", async () => {
		const onSelectModel = vi.fn();
		const onSelectEffort = vi.fn();
		const root = await render(<IOSReviewerPicker {...props({ onSelectModel, onSelectEffort })} />);
		const [reviewer, model, effort] = root.findAllByType("Picker" as never);
		expect(reviewer.props).toMatchObject({ label: "Reviewer", selection: "" });
		const options = reviewer.findAllByType("Label" as never);
		expect(options.map((option) => [option.props.title, option.props.modifiers[0].args[0]])).toEqual([["Project default · Codex", ""], ["Codex", "codex"], ["OpenCode", "opencode"]]);
		const choices = (picker: typeof model) => picker.findAllByType("SwiftText" as never).map((option) => [option.props.children, option.props.modifiers[0].args[0]]);
		expect(model.props).toMatchObject({ label: "Model", selection: "gpt-5" });
		expect(choices(model)).toEqual([["GPT-5", "gpt-5"], ["GPT-5 mini", "gpt-5-mini"]]);
		expect(effort.props).toMatchObject({ label: "Effort", selection: "high" });
		expect(choices(effort)).toEqual([["Low", "low"], ["High", "high"]]);
		act(() => model.props.onSelectionChange("gpt-5-mini"));
		expect(onSelectModel).toHaveBeenCalledWith("gpt-5-mini");
		act(() => effort.props.onSelectionChange("low"));
		expect(onSelectEffort).toHaveBeenCalledWith("low");
	});

	it("tags the way back to the agent's model with no id", async () => {
		const model = { title: "Model", value: "gpt-5-mini", valueLabel: "GPT-5 mini", options: [{ id: "", label: "Use agent model" }, { id: "gpt-5", label: "GPT-5" }, { id: "gpt-5-mini", label: "GPT-5 mini" }] };
		const root = await render(<IOSReviewerPicker {...props({ model, effort: undefined })} />);
		const pickers = root.findAllByType("Picker" as never);
		expect(pickers).toHaveLength(2);
		expect(pickers[1].findAllByType("SwiftText" as never)[0].props).toMatchObject({ children: "Use agent model", modifiers: [{ modifier: "tag", args: [""] }] });
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
