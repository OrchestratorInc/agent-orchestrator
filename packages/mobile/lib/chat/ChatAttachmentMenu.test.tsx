import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { forwardRef, useImperativeHandle, type ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	os: "ios",
	show: vi.fn(),
	tap: vi.fn(),
}));
vi.mock("@expo/ui/community/menu", async () => {
	const { createElement } = await import("react");
	// The Android trigger opens the menu through its ref.
	const MenuView = forwardRef<unknown, { children?: ReactNode }>(function MenuView(props, ref) {
		useImperativeHandle(ref, () => ({ show: fixtures.show }));
		return createElement("MenuView", props, props.children);
	});
	return { MenuView };
});
vi.mock("react-native", () => ({
	Platform: { get OS() { return fixtures.os; } },
	Pressable: "Pressable",
	StyleSheet: { create: <T,>(styles: T) => styles },
	View: "View",
}));
vi.mock("../AppSymbol", () => ({ AppSymbol: "AppSymbol" }));
vi.mock("../haptics", () => ({ haptics: { tap: fixtures.tap } }));
vi.mock("../pressFeedback", () => ({ usePressFeedback: () => ({ android_ripple: { color: "ripple", borderless: true }, pressed: undefined }) }));
vi.mock("../ThemeProvider", () => ({
	useTheme: () => ({ textSecondary: "label" }),
	useThemeState: () => ({ scheme: "dark" }),
}));

import { ChatAttachmentMenu } from "./ChatAttachmentMenu";

let renderer: ReactTestRenderer | undefined;
beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.os = "ios";
	fixtures.show.mockClear();
	fixtures.tap.mockClear();
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
});
afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

function render(props: Partial<Parameters<typeof ChatAttachmentMenu>[0]> = {}) {
	const handlers = { onChoosePhoto: vi.fn(), onChooseFile: vi.fn() };
	act(() => { renderer = create(<ChatAttachmentMenu disabled={false} canAttachFile {...handlers} {...props} />); });
	return handlers;
}
const menu = () => renderer!.root.findByType("MenuView" as never);

describe("ChatAttachmentMenu", () => {
	it("offers only native photo and supported file actions", () => {
		render();
		expect(menu().props.actions).toEqual([
			{ id: "photo", title: "Choose Photo", image: "photo" },
			{ id: "file", title: "Choose File", image: "doc" },
		]);
		expect(menu().props.colorScheme).toBe("dark");
		act(() => renderer!.update(<ChatAttachmentMenu disabled={false} canAttachFile={false} onChoosePhoto={vi.fn()} onChooseFile={vi.fn()} />));
		expect(menu().props.actions.map((action: { id: string }) => action.id)).toEqual(["photo"]);
	});

	it("routes the selected native action to its picker", () => {
		const { onChoosePhoto, onChooseFile } = render();
		act(() => menu().props.onPressAction({ nativeEvent: { event: "file" } }));
		expect(onChooseFile).toHaveBeenCalledOnce();
		expect(onChoosePhoto).not.toHaveBeenCalled();
		// Opening a picker is navigation, not a moment for a haptic.
		expect(fixtures.tap).not.toHaveBeenCalled();
	});

	it("leaves the tap to SwiftUI's menu label on iOS", () => {
		render();
		const trigger = menu().findByProps({ accessibilityLabel: "Attach" });
		expect(trigger.type).toBe("View");
		expect(trigger.props.accessibilityRole).toBe("button");
	});

	it("opens the Compose menu from a rippling button on Android", () => {
		fixtures.os = "android";
		render();
		const trigger = menu().findByProps({ accessibilityLabel: "Attach" });
		expect(trigger.type).toBe("Pressable");
		expect(trigger.props.android_ripple).toEqual({ color: "ripple", borderless: true });
		act(() => trigger.props.onPress());
		expect(fixtures.show).toHaveBeenCalledOnce();
		expect(fixtures.tap).not.toHaveBeenCalled();
	});

	it("shows a disabled button and no menu while attachments are off", () => {
		render({ disabled: true });
		expect(renderer!.root.findAllByType("MenuView" as never)).toHaveLength(0);
		const trigger = renderer!.root.findByProps({ accessibilityLabel: "Attach" });
		expect(trigger.props.accessibilityState).toEqual({ disabled: true });
	});
});
