import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });
const { paste } = vi.hoisted(() => ({ paste: { handler: null as null | ((assets: unknown[]) => void) } }));
const { mounts } = vi.hoisted(() => ({ mounts: [] as string[] }));
vi.mock("react-native", async () => {
	const React = await import("react");
	// Records each mount, so a face that reuses the previous face's view shows up.
	const Pressable = ({ children, ...props }: { children?: React.ReactNode; accessibilityLabel?: string }) => {
		React.useEffect(() => { mounts.push(props.accessibilityLabel ?? ""); }, []);
		return React.createElement("Pressable", props, children);
	};
	return {
		Platform: { OS: "android" },
		ActivityIndicator: "ActivityIndicator", Image: "Image", Pressable, ScrollView: "ScrollView", Text: "Text", TextInput: "TextInput", View: "View",
		StyleSheet: { create: (styles: unknown) => styles, hairlineWidth: 1, absoluteFill: {} },
	};
});
vi.mock("react-native-reanimated", () => ({ default: { View: "AnimatedView" }, useAnimatedStyle: () => ({}) }));
vi.mock("react-native-keyboard-controller", () => ({ useReanimatedKeyboardAnimation: () => ({ progress: { value: 0 } }) }));
vi.mock("expo-document-picker", () => ({}));
vi.mock("expo-image-picker", () => ({ launchImageLibraryAsync: vi.fn() }));
vi.mock("../icons", () => ({ Feather: "Feather" }));
vi.mock("../haptics", () => ({ haptics: { tap: vi.fn(), success: vi.fn() } }));
vi.mock("../ThemeProvider", () => ({ useTheme: () => ({}), useThemedStyles: (factory: (theme: object) => unknown) => factory({}) }));
vi.mock("../voice/MicKey", () => ({ MicKey: "MicKey" }));
vi.mock("../voice/useVoiceInput", () => ({ useVoiceInput: () => ({}) }));
vi.mock("./ChatAttachmentMenu", () => ({ ChatAttachmentMenu: "ChatAttachmentMenu" }));
vi.mock("../ImageViewer", () => ({ ImageViewer: "ImageViewer" }));
vi.mock("./useImagePaste", () => ({ useImagePaste: (handler: typeof paste.handler) => { paste.handler = handler; return () => {}; } }));
vi.mock("./composer-glass", () => ({ ComposerGlass: "ComposerGlass", composerGlassSupported: false }));
vi.mock("../pressFeedback", () => ({ usePressFeedback: () => ({ android_ripple: undefined, pressed: {} }) }));
vi.mock("../AppSymbol", () => ({ AppSymbol: "AppSymbol" }));

import type { ComponentProps } from "react";
import { ReviewerComposer } from "./ReviewerComposer";

const props = (busy: boolean) => ({
	attachmentKey: "reviewer.host.review-1", busy, stopped: false, restingInset: 0,
	onSend: vi.fn(async () => "send-id"), onAcknowledgeSend: vi.fn(async () => {}), onInterrupt: vi.fn(async () => {}),
} as ComponentProps<typeof ReviewerComposer>);
let renderer: ReactTestRenderer;
afterEach(() => { if (renderer) act(() => renderer.unmount()); mounts.length = 0; vi.clearAllMocks(); });

// Stop and send are each a round, clipping button (the clip keeps the ripple
// round). Reusing one view for both on Android leaves the send arrow undrawn
// when the reviewer finishes: a clipping view skips a child that resized in the
// same update as its own fill. Each face gets a view of its own.
it("mounts a fresh send button when the reviewer finishes, rather than reusing the stop button's view", async () => {
	await act(async () => { renderer = create(<ReviewerComposer {...props(true)} />); });
	expect(mounts).toContain("Stop reviewer");
	await act(async () => { renderer.update(<ReviewerComposer {...props(false)} />); });
	expect(renderer.root.findByProps({ accessibilityLabel: "Send reply" })).toBeTruthy();
	expect(mounts).toContain("Send reply");
});
