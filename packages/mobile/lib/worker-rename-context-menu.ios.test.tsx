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

it("lists every action, and Archive as destructive", () => {
	const menu = mount().root.findByType("MenuView" as never);
	expect(menu.props.actions.map((action: { id: string }) => action.id)).toEqual(["open", "unpin", "delete"]);
	expect(menu.props.actions.at(-1).attributes).toEqual({ destructive: true });
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
