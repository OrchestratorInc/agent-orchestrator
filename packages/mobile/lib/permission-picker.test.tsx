import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	theme: new Proxy({}, { get: (_target, key) => `#${String(key)}` }),
	tap: vi.fn(),
	select: vi.fn(),
}));

vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		BackHandler: { addEventListener: () => ({ remove: () => {} }) },
		Keyboard: { addListener: () => ({ remove: () => {} }) },
		StyleSheet: { create: <T,>(styles: T) => styles, hairlineWidth: 1 },
		Pressable: ({ children, style, ...props }: { children?: React.ReactNode; style?: unknown }) =>
			React.createElement("Pressable", { ...props, style: typeof style === "function" ? style({ pressed: false }) : style }, children),
		Text: host("Text"),
		View: host("View"),
	};
});
// Desktop's permission icons, drawn by name.
vi.mock("lucide-react-native/icons/hand", () => ({ default: "Hand" }));
vi.mock("lucide-react-native/icons/pencil-line", () => ({ default: "PencilLine" }));
vi.mock("lucide-react-native/icons/shield-alert", () => ({ default: "ShieldAlert" }));
vi.mock("lucide-react-native/icons/shield-check", () => ({ default: "ShieldCheck" }));
vi.mock("./ThemeProvider", () => ({ useTheme: () => fixtures.theme, useThemedStyles: (make: (theme: object) => object) => make(fixtures.theme) }));
vi.mock("./haptics", () => ({ haptics: { tap: fixtures.tap, select: fixtures.select } }));
vi.mock("./composer-overlay", async () => {
	const React = await import("react");
	return {
		ComposerOverlay: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("ComposerOverlay", props, children),
		OverlayHeading: (props: object) => React.createElement("OverlayHeading", props),
		overlayCardStyle: () => ({}),
	};
});
vi.mock("./effort-slider-host", () => ({ useWarmAfterIdle: () => false }));

import { PermissionPicker, PermissionPickerButton, PermissionPickerHost, type PermissionChoice } from "./permission-picker";
import { createEffortSliderSwitch } from "./useEffortSlider";

const modes = (selected: string): PermissionChoice[] => [
	{ value: "accept-edits", label: "Accept edits", description: "Edits files without asking", look: "accept-edits" },
	{ value: "auto", label: "Auto-approve", description: "Asks only about risky actions", look: "auto" },
	{ value: "bypass-permissions", label: "Bypass permissions", description: "Never asks before acting", look: "bypass-permissions" },
].map((choice) => ({ ...choice, look: choice.look as PermissionChoice["look"], selected: choice.value === selected }));

let renderer: ReactTestRenderer | undefined;
beforeEach(() => { vi.useFakeTimers(); vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true); fixtures.tap.mockClear(); fixtures.select.mockClear(); });
afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; vi.useRealTimers(); vi.unstubAllGlobals(); });

const hostType = (node: ReactTestInstance): string => (typeof node.type === "string" ? node.type : "");
const all = (type: string) => renderer!.root.findAll((node) => hostType(node) === type);
const texts = (node: ReactTestInstance) => node.findAll((child) => hostType(child) === "Text").map((child) => child.props.children);

describe("permission picker", () => {
	// Desktop's permission rows: the mode's icon, its name and what it lets the
	// agent do, never asking in the warning colour.
	it("lists the modes as desktop does, on the veil alone", () => {
		act(() => { renderer = create(<PermissionPicker choices={modes("auto")} onChoose={() => {}} />); });
		expect(all("OverlayHeading")).toHaveLength(0);
		const rows = all("Pressable");
		expect(rows.map((row) => texts(row))).toEqual([
			["Accept edits", "Edits files without asking"],
			["Auto-approve", "Asks only about risky actions"],
			["Bypass permissions", "Never asks before acting"],
		]);
		const icon = (row: ReactTestInstance) => row.findAll((node) => ["Hand", "PencilLine", "ShieldCheck", "ShieldAlert"].includes(hostType(node)))[0];
		expect(rows.map((row) => hostType(icon(row)))).toEqual(["PencilLine", "ShieldCheck", "ShieldAlert"]);
		expect(rows.map((row) => icon(row).props.color)).toEqual(["#textSecondary", "#textPrimary", "#amber"]);
		// One line each, so every row is the same height.
		expect(rows.map((row) => row.findAll((node) => hostType(node) === "Text")[1].props.numberOfLines)).toEqual([1, 1, 1]);
		// The mode in force sits in a low wash, not ticked.
		expect(rows.map((row) => (row.props.style as { backgroundColor?: string }[]).some((style) => style?.backgroundColor === "#accentTint"))).toEqual([false, true, false]);
		expect(rows.map((row) => row.props.accessibilityState)).toEqual([{ checked: false }, { checked: true }, { checked: false }]);
	});

	// A screen narrower than the list must not push it past the composer.
	it("caps the list at the width the overlay leaves", () => {
		act(() => { renderer = create(<PermissionPicker choices={modes("auto")} onChoose={() => {}} />); });
		const list = renderer!.root.findAll((node) => hostType(node) === "View" && node.props.testID === "permission-picker")[0];
		expect(list.props.style).toMatchObject({ width: 360, maxWidth: "100%" });
	});

	it("draws an agent's own tier, which maps to no mode, as asking", () => {
		act(() => { renderer = create(<PermissionPicker choices={[{ value: "plan", label: "Plan", selected: true }]} onChoose={() => {}} />); });
		expect(all("Hand")).toHaveLength(1);
	});

	it("chooses a mode, ticking only on a change", () => {
		const onChoose = vi.fn();
		act(() => { renderer = create(<PermissionPicker choices={modes("auto")} onChoose={onChoose} />); });
		act(() => all("Pressable")[2].props.onPress());
		expect(onChoose).toHaveBeenLastCalledWith("bypass-permissions");
		expect(fixtures.select).toHaveBeenCalledOnce();
		act(() => all("Pressable")[1].props.onPress());
		expect(onChoose).toHaveBeenLastCalledWith("auto");
		expect(fixtures.select).toHaveBeenCalledOnce();
	});
});

describe("permission button", () => {
	it("shows the mode in force by its icon, and never asking in the warning colour", () => {
		const slider = createEffortSliderSwitch();
		act(() => { renderer = create(<PermissionPickerButton slider={slider} choices={modes("auto")} disabled={false} testID="perm" />); });
		expect(all("ShieldCheck")[0].props.color).toBe("#textSecondary");
		expect(all("Pressable")[0].props.accessibilityLabel).toBe("Permissions, Auto-approve");
		act(() => renderer!.update(<PermissionPickerButton slider={slider} choices={modes("bypass-permissions")} disabled={false} testID="perm" />));
		expect(all("ShieldAlert")[0].props.color).toBe("#amber");
	});

	// As the effort gauge: it toggles as the finger lands, once per touch.
	it("opens and closes its picker as a finger lands, putting anything else away first", () => {
		const slider = createEffortSliderSwitch();
		const onPress = vi.fn();
		act(() => { renderer = create(<PermissionPickerButton slider={slider} choices={modes("auto")} disabled={false} onPress={onPress} testID="perm" />); });
		const button = () => all("Pressable")[0];
		act(() => button().props.onPressIn());
		expect(slider.isOpen()).toBe(true);
		expect(onPress).toHaveBeenCalledOnce();
		expect(fixtures.tap).toHaveBeenCalledOnce();
		act(() => button().props.onPress());
		expect(slider.isOpen()).toBe(true);
		expect(button().props.accessibilityState).toEqual({ expanded: true, disabled: false });
		// A screen reader's activation, with no touch before it.
		act(() => button().props.onPress());
		expect(slider.isOpen()).toBe(false);
	});

	// A finger sliding off and back on fires onPressIn again.
	it("toggles once per touch, even when the finger slides off and back on", () => {
		const slider = createEffortSliderSwitch();
		act(() => { renderer = create(<PermissionPickerButton slider={slider} choices={modes("auto")} disabled={false} testID="perm" />); });
		const button = () => all("Pressable")[0];
		act(() => button().props.onPressIn());
		act(() => button().props.onPressIn());
		expect(slider.isOpen()).toBe(true);
	});

	// A touch let go outside the button gets no onPress to clear it.
	it("still answers a screen reader after a touch released outside", () => {
		const slider = createEffortSliderSwitch();
		act(() => { renderer = create(<PermissionPickerButton slider={slider} choices={modes("auto")} disabled={false} testID="perm" />); });
		const button = () => all("Pressable")[0];
		act(() => button().props.onPressIn());
		act(() => { button().props.onTouchEnd(); vi.runAllTimers(); });
		act(() => button().props.onPress());
		expect(slider.isOpen()).toBe(false);
	});
});

describe("permission picker host", () => {
	it("shows the picker while open, closes it on a pick, and has no timeout", () => {
		const slider = createEffortSliderSwitch();
		slider.setBottom(120);
		const onChoose = vi.fn();
		act(() => { renderer = create(<PermissionPickerHost slider={slider} choices={modes("auto")} busy={false} onChoose={onChoose} />); });
		expect(all("ComposerOverlay")).toHaveLength(0);
		act(() => slider.setOpen(true));
		expect(all("ComposerOverlay")[0].props).toMatchObject({ visible: true, bottom: 120 });
		act(() => { vi.advanceTimersByTime(60_000); });
		expect(slider.isOpen()).toBe(true);
		act(() => all("Pressable")[0].props.onPress());
		expect(onChoose).toHaveBeenCalledWith("accept-edits");
		expect(slider.isOpen()).toBe(false);
	});

	it("closes the composer's switch when the host goes away", () => {
		const slider = createEffortSliderSwitch();
		slider.setBottom(120);
		act(() => { renderer = create(<PermissionPickerHost slider={slider} choices={modes("auto")} busy={false} onChoose={() => {}} />); });
		act(() => slider.setOpen(true));
		act(() => renderer!.update(<></>));
		expect(slider.isOpen()).toBe(false);
	});
});
