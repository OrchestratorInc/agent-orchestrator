import { act, create, type ReactTestRenderer } from "react-test-renderer";
import type { ComponentProps } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Text } from "react-native";
import { ChatAttachmentMenu as AndroidAttachmentMenu } from "./ChatAttachmentMenu.android";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

const { MenuView, Feather, View } = vi.hoisted(() => ({
	MenuView: "MenuView",
	Feather: vi.fn(),
	View: "View",
}));

vi.mock("@expo/ui/community/menu", () => ({ MenuView }));
vi.mock("@expo/ui/community/bottom-sheet", () => ({ default: "BottomSheet", BottomSheetView: "BottomSheetView" }));
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ bottom: 0 }) }));
// The icon is a Lucide component now (see lib/icons), and Lucide's React Native
// build is ESM that Vitest cannot parse in this environment — the component is
// all this test needs to know about.
vi.mock("../icons", () => ({ Feather }));
vi.mock("react-native", () => ({
	StyleSheet: { create: (styles: unknown) => styles },
	View,
	Pressable: "Pressable", Text: "Text",
}));
vi.mock("../haptics", () => ({ haptics: { tap: vi.fn() } }));
vi.mock("../ThemeProvider", () => ({
	useTheme: () => ({ textFaint: "dim", textSecondary: "label" }),
	useThemedStyles: (factory: (theme: Record<string, string>) => unknown) => factory({
		textFaint: "dim",
		textSecondary: "label",
	}),
}));

import { ChatAttachmentMenu } from "./ChatAttachmentMenu";

let native: ReactTestRenderer;
function nativeMenu(props: ComponentProps<typeof ChatAttachmentMenu>) {
	act(() => { native = create(<ChatAttachmentMenu {...props} />); });
	return native.root.findByType(MenuView as never);
}

describe("ChatAttachmentMenu", () => {
	it("offers only native photo and supported file actions", () => {
		const element = nativeMenu({
			disabled: false,
			canAttachPhoto: true,
			canAttachFile: true,
			onChoosePhoto: vi.fn(),
			onChooseFile: vi.fn(),
		});

		expect(element.props.actions).toEqual([
			{ id: "photo", title: "Choose Photo", image: "photo" },
			{ id: "file", title: "Choose File", image: "doc" },
		]);
	});

	it("routes the selected native action to its picker", () => {
		const onChoosePhoto = vi.fn();
		const onChooseFile = vi.fn();
		const element = nativeMenu({ disabled: false, canAttachPhoto: true, canAttachFile: true, onChoosePhoto, onChooseFile });

		element.props.onPressAction({ nativeEvent: { event: "file" } });

		expect(onChooseFile).toHaveBeenCalledOnce();
		expect(onChoosePhoto).not.toHaveBeenCalled();
	});

	it("hides the trigger when neither attachment can be delivered", () => {
		act(() => { native = create(<ChatAttachmentMenu disabled={false} canAttachPhoto={false} canAttachFile={false} onChoosePhoto={vi.fn()} onChooseFile={vi.fn()} />); });
		expect(native.root.findAllByProps({ accessibilityLabel: "Attach" })).toHaveLength(0);
	});

	it.each([
		["photo", false, true],
		["file", true, false],
	] as const)("ignores unsupported native %s actions", (action, canAttachPhoto, canAttachFile) => {
		const onChoosePhoto = vi.fn();
		const onChooseFile = vi.fn();
		const element = nativeMenu({ disabled: false, canAttachPhoto, canAttachFile, onChoosePhoto, onChooseFile });
		element.props.onPressAction({ nativeEvent: { event: action } });
		expect(onChoosePhoto).not.toHaveBeenCalled();
		expect(onChooseFile).not.toHaveBeenCalled();
	});

	it.each(["disabled", "unsupported"])("ignores a retained native event after attachments become %s", (reason) => {
		const p = { disabled: false, canAttachPhoto: true, canAttachFile: true, onChoosePhoto: vi.fn(), onChooseFile: vi.fn() };
		const choose = nativeMenu(p).props.onPressAction;
		act(() => native.update(<ChatAttachmentMenu {...p} disabled={reason === "disabled"} canAttachPhoto={reason !== "unsupported"} canAttachFile={reason !== "unsupported"} />));
		choose({ nativeEvent: { event: "photo" } });
		choose({ nativeEvent: { event: "file" } });
		expect(p.onChoosePhoto).not.toHaveBeenCalled();
		expect(p.onChooseFile).not.toHaveBeenCalled();
	});
});

let android: ReactTestRenderer;
afterEach(() => { if (native) act(() => native.unmount()); if (android) act(() => android.unmount()); vi.useRealTimers(); });

describe("Android attachment menu", () => {
	it("hides the trigger when no attachment can be delivered", () => {
		act(() => { android = create(<AndroidAttachmentMenu disabled={false} canAttachPhoto={false} canAttachFile={false} onChoosePhoto={vi.fn()} onChooseFile={vi.fn()} />); });
		expect(android.root.findAllByProps({ accessibilityLabel: "Attach" })).toHaveLength(0);
	});

	it("hides unsupported photo choices while retaining supported files", () => {
		act(() => { android = create(<AndroidAttachmentMenu disabled={false} canAttachPhoto={false} canAttachFile={true} onChoosePhoto={vi.fn()} onChooseFile={vi.fn()} />); });
		const labels = android.root.findAllByType(Text).map((node) => node.props.children);
		expect(labels).not.toContain("Choose photo");
		expect(labels).toContain("Choose file");
	});

	it("does not launch pickers through disabled sheet callbacks", () => {
		vi.useFakeTimers();
		const onChoosePhoto = vi.fn();
		const onChooseFile = vi.fn();
		act(() => { android = create(<AndroidAttachmentMenu disabled canAttachPhoto canAttachFile onChoosePhoto={onChoosePhoto} onChooseFile={onChooseFile} />); });
		act(() => {
			android.root.findAllByType(Text).filter((node) => ["Choose photo", "Choose file"].includes(node.props.children)).forEach((node) => node.parent!.props.onPress());
			vi.runAllTimers();
		});
		expect(onChoosePhoto).not.toHaveBeenCalled();
		expect(onChooseFile).not.toHaveBeenCalled();
	});

	it("ignores retained sheet callbacks after attachment support disappears", () => {
		vi.useFakeTimers();
		const p = { disabled: false, canAttachPhoto: true, canAttachFile: true, onChoosePhoto: vi.fn(), onChooseFile: vi.fn() };
		act(() => { android = create(<AndroidAttachmentMenu {...p} />); });
		const callbacks = android.root.findAllByType(Text).filter((node) => ["Choose photo", "Choose file"].includes(node.props.children)).map((node) => node.parent!.props.onPress);
		act(() => android.update(<AndroidAttachmentMenu {...p} canAttachPhoto={false} canAttachFile={false} />));
		act(() => { callbacks.forEach((choose) => choose()); vi.runAllTimers(); });
		expect(p.onChoosePhoto).not.toHaveBeenCalled();
		expect(p.onChooseFile).not.toHaveBeenCalled();
	});
});
