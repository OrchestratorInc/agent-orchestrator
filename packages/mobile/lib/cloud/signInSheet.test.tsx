import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { expect, it, vi } from "vitest";
const state = vi.hoisted(() => ({ signInWithWorkOS: vi.fn(), done: vi.fn() }));
vi.mock("./authStore", () => ({ useCloudAuth: () => ({ signInWithWorkOS: state.signInWithWorkOS }) }));
vi.mock("react-native", () => ({ ActivityIndicator: "ActivityIndicator", Pressable: "Pressable", Text: "Text", TextInput: "TextInput", View: "View", StyleSheet: { create: (styles: unknown) => styles } }));
vi.mock("@expo/vector-icons", () => ({ Feather: "Feather" }));
vi.mock("../ThemeProvider", () => ({ useTheme: () => ({}), useThemedStyles: (make: (theme: unknown) => unknown) => make({}) }));
vi.mock("../haptics", () => ({ haptics: { tap: vi.fn() } }));
vi.mock("../ui", () => ({ SheetScreen: "SheetScreen" }));
import { CloudSignInSheet } from "../CloudSignInSheet";

it("does not navigate when a dismissed sign-in sheet receives late browser success", async () => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	let finish!: (value: boolean) => void;
	state.signInWithWorkOS.mockImplementation(() => new Promise<boolean>((resolve) => { finish = resolve; }));
	let renderer!: ReactTestRenderer;
	await act(async () => { renderer = create(<CloudSignInSheet onDone={state.done} onClose={() => {}} />); });
	let pending!: Promise<void>;
	await act(async () => { pending = renderer.root.findAllByType("Pressable" as never)[0]!.props.onPress(); });
	await act(async () => { renderer.unmount(); });
	await act(async () => { finish(true); await pending; await Promise.resolve(); });
	expect(state.done).not.toHaveBeenCalled();
	vi.unstubAllGlobals();
});
