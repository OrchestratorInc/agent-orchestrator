import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

// Keep the row wrapper real and replace only native rendering boundaries.
// The layout contract passed across these boundaries is what regressed.
vi.mock("react-native", () => ({ Pressable: "Pressable" }));
vi.mock("@expo/ui", () => ({ Host: "NativeHost" }));
vi.mock("@expo/ui/swift-ui", async () => {
	const { createElement } = await import("react");
	return {
		Button: "NativeButton",
		ContextMenu: Object.assign((props: { children: React.ReactNode }) => createElement("NativeContextMenu", props), {
			Trigger: "NativeTrigger", Items: "NativeItems",
		}),
		RNHostView: "NativeRNHost",
	};
});
vi.mock("@expo/ui/community/menu", () => ({ MenuView: "CommunityMenu" }));

import { WorkerRowContextMenu } from "./worker-rename-context-menu.ios";

let renderer: ReactTestRenderer | undefined;
const actions = [
	{ id: "open" as const, title: "Open" },
	{ id: "rename" as const, title: "Rename" },
	{ id: "delete" as const, title: "Archive session", destructive: true },
];
const onPress = vi.fn();
const onAction = vi.fn();
function mount(title = "Short task") {
	act(() => { renderer = create(
		<WorkerRowContextMenu
			actions={actions} onAction={onAction} onPress={onPress}
			gestureRef={{ current: undefined }}
			accessibilityLabel={title} accessibilityHint="Swipe for actions. Long press for more."
			style={{ minHeight: 76, paddingHorizontal: 16 }} pressedStyle={{ opacity: 0.6 }}
		>{title}</WorkerRowContextMenu>,
	); });
	return renderer!.root;
}
beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	onPress.mockClear(); onAction.mockClear();
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
});
afterEach(() => {
	act(() => renderer?.unmount()); renderer = undefined;
	vi.restoreAllMocks(); vi.unstubAllGlobals();
});

it.each([
	["short title", "Short task"],
	["long title", "A long worker title ".repeat(30)],
])("inherits list width instead of measuring a %s", (_label, title) => {
	const root = mount(title);
	expect(root.findAllByType("NativeHost" as never).map(node => ({
		matchContents: node.props.matchContents, style: node.props.style,
	}))).toEqual([{ matchContents: { vertical: true }, style: { width: "100%" } }]);
	expect(root.findAllByType("NativeRNHost" as never).map(node => node.props.matchContents)).toEqual([{ vertical: true }]);
});

it("retains row tap, accessibility and pressed styling inside the long-press trigger", () => {
	const root = mount();
	const pressable = root.findByType("Pressable" as never);
	act(() => pressable.props.onPress());
	expect(onPress).toHaveBeenCalledOnce();
	expect(onAction).not.toHaveBeenCalled();
	expect(pressable.props.accessibilityLabel).toBe("Short task");
	expect(pressable.props.accessibilityRole).toBe("button");
	expect(pressable.props.style({ pressed: true })).toEqual([{ minHeight: 76, paddingHorizontal: 16 }, { opacity: 0.6 }]);
});

it("retains native long-press actions, SF Symbols and destructive archive routing", () => {
	const root = mount();
	const buttons = root.findAllByType("NativeButton" as never);
	expect(buttons.map(node => ({ label: node.props.label, image: node.props.systemImage, role: node.props.role }))).toEqual([
		{ label: "Open", image: "arrow.forward", role: undefined },
		{ label: "Rename", image: "pencil", role: undefined },
		{ label: "Archive session", image: "archivebox", role: "destructive" },
	]);
	act(() => buttons[1].props.onPress());
	expect(onAction).toHaveBeenCalledExactlyOnceWith("rename");
	expect(onPress).not.toHaveBeenCalled();
});
