import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { createElement } from "react";
import { afterEach, expect, it, vi } from "vitest";

vi.mock("react-native", () => ({ Platform: { OS: "ios" }, Pressable: "Pressable" }));
vi.mock("@expo/ui/community/menu", () => ({ MenuView: "MenuView" }));
vi.mock("./ThemeProvider", () => ({ useTheme: () => ({ bgElevatedHover: "hover", accentTint: "accent-tint" }) }));

import { WorkerRowContextMenu } from "./worker-rename-context-menu.ios";

let renderer: ReactTestRenderer | undefined;
afterEach(() => act(() => renderer?.unmount()));
function mount(onAction = vi.fn()) {
	Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
	act(() => {
		renderer = create(
			<WorkerRowContextMenu
				onPress={vi.fn()}
				onAction={onAction}
				actions={[{ id: "open", title: "Open" }, { id: "unpin", title: "Unpin" }, { id: "delete", title: "Archive session", destructive: true }]}
				accessibilityLabel="Polish workers"
				style={{ minHeight: 76 }}
			>
				{createElement("RowContent")}
			</WorkerRowContextMenu>,
		);
	});
	return renderer!;
}

it("lists every action with its SF Symbol, and Archive as destructive", () => {
	const menu = mount().root.findByType("MenuView" as never);
	expect(menu.props.shouldOpenOnLongPress).toBe(true);
	expect(menu.props.actions).toEqual([
		{ id: "open", title: "Open", image: "arrow.forward", attributes: undefined },
		{ id: "unpin", title: "Unpin", image: "pin.slash", attributes: undefined },
		{ id: "delete", title: "Archive session", image: "archivebox", attributes: { destructive: true } },
	]);
});

// VoiceOver cannot long-press the row into its menu, so the menu's actions are
// in the Actions rotor too.
it("offers the menu's actions to VoiceOver", () => {
	const onAction = vi.fn();
	const row = mount(onAction).root.findByType("Pressable" as never);
	expect(row.props.accessibilityActions).toEqual([{ name: "unpin", label: "Unpin" }, { name: "delete", label: "Archive session" }]);
	act(() => row.props.onAccessibilityAction({ nativeEvent: { actionName: "unpin" } }));
	expect(onAction).toHaveBeenCalledWith("unpin");
});

it("highlights the row as a table cell does while pressed", () => {
	const row = mount().root.findByType("Pressable" as never);
	expect(row.props.style({ pressed: true })).toEqual([{ minHeight: 76 }, { backgroundColor: "hover" }]);
	// SwiftUI's context menu owns the long press on iOS.
	expect(row.props.onLongPress).toBeUndefined();
});
