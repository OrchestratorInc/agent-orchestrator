import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

const { copy } = vi.hoisted(() => ({ copy: vi.fn(() => Promise.resolve()) }));
vi.mock("react-native", () => ({
	Modal: "Modal",
	Pressable: "Pressable",
	StyleSheet: { create: (styles: unknown) => styles },
	Text: "Text",
	TextInput: "TextInput",
	View: "View",
}));
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ top: 20, bottom: 12 }) }));
vi.mock("../icons", () => ({ Feather: "Feather" }));
vi.mock("expo-clipboard", () => ({ setStringAsync: copy }));
vi.mock("../haptics", () => ({ haptics: { success: vi.fn() } }));
vi.mock("../ThemeProvider", () => ({
	useTheme: () => ({}),
	useThemedStyles: (factory: (theme: Record<string, string>) => unknown) => factory({}),
}));

import { ResponseSelectionModal } from "./ResponseSelectionModal";

it("copies only the range selected from a read-only response", () => {
	const onClose = vi.fn();
	let renderer!: ReactTestRenderer;
	act(() => { renderer = create(<ResponseSelectionModal text="Alpha beta gamma" onClose={onClose} />); });

	const input = renderer.root.findByProps({ accessibilityLabel: "Response text" });
	expect(input.props.value).toBe("Alpha beta gamma");
	expect(input.props.multiline).toBe(true);
	expect(input.props.editable).toBe(false);
	expect(input.props.showSoftInputOnFocus).toBe(false);

	let copySelection = renderer.root.findByProps({ accessibilityLabel: "Copy selected text" });
	expect(copySelection.props.disabled).toBe(true);
	act(() => input.props.onSelectionChange({ nativeEvent: { selection: { start: 6, end: 10 } } }));
	copySelection = renderer.root.findByProps({ accessibilityLabel: "Copy selected text" });
	expect(copySelection.props.disabled).toBe(false);
	act(() => input.props.onSelectionChange({ nativeEvent: { selection: { start: 10, end: 10 } } }));
	copySelection = renderer.root.findByProps({ accessibilityLabel: "Copy selected text" });
	act(() => copySelection.props.onPress());
	expect(copy).toHaveBeenCalledWith("beta");
	act(() => renderer.root.findByProps({ accessibilityLabel: "Close text selection" }).props.onPress());
	expect(onClose).toHaveBeenCalledOnce();
	act(() => renderer.unmount());
});
