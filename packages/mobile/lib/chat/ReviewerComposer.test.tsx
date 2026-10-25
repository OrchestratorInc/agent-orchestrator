import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, expect, it, vi } from "vitest";
import type { ComponentProps } from "react";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });
vi.mock("react-native", () => ({
	Platform: { OS: "android" },
	ActivityIndicator: "ActivityIndicator", Image: "Image", Pressable: "Pressable", ScrollView: "ScrollView", Text: "Text", TextInput: "TextInput", View: "View",
	StyleSheet: { create: (styles: unknown) => styles, hairlineWidth: 1, absoluteFill: {} },
}));
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
vi.mock("./composer-glass", () => ({ ComposerGlass: "ComposerGlass", composerGlassSupported: false }));

import { launchImageLibraryAsync } from "expo-image-picker";
import { ChatAttachmentMenu } from "./ChatAttachmentMenu";
import { ReviewerComposer } from "./ReviewerComposer";

const props = () => ({
	attachmentKey: "reviewer.host.review-1", busy: false, stopped: false, restingInset: 0,
	onSend: vi.fn(async () => "send-id"), onAcknowledgeSend: vi.fn(async () => {}), onInterrupt: vi.fn(async () => {}),
} as ComponentProps<typeof ReviewerComposer>);
let renderer: ReactTestRenderer;
afterEach(() => { if (renderer) act(() => renderer.unmount()); vi.clearAllMocks(); });

it("keeps a photo picked while Android remounts the reviewer chat, labelled by its bytes", async () => {
	const JPEG = "/9j/4AAQSkZJRgAB";
	let pick!: (result: unknown) => void;
	vi.mocked(launchImageLibraryAsync).mockImplementation(() => new Promise((resolve) => { pick = resolve; }) as never);
	const p = props();
	await act(async () => { renderer = create(<ReviewerComposer {...p} />); });
	act(() => renderer.root.findByType(ChatAttachmentMenu).props.onChoosePhoto());
	act(() => renderer.unmount());
	await act(async () => pick({ canceled: false, assets: [{ uri: "file:///shot.png", fileName: "shot.png", width: 1, height: 1, base64: JPEG, mimeType: "image/png" }] }));
	await act(async () => { renderer = create(<ReviewerComposer {...p} />); });
	expect(renderer.root.findAllByProps({ accessibilityLabel: "Remove shot.png" })).toHaveLength(1);
	await act(async () => renderer.root.findByProps({ accessibilityLabel: "Send reply" }).props.onPress());
	expect(p.onSend).toHaveBeenCalledExactlyOnceWith("", [{ mimeType: "image/jpeg", data: JPEG }], []);
	expect(renderer.root.findAllByProps({ accessibilityLabel: "Remove shot.png" })).toHaveLength(0);
});

it("clears only what a send submitted, keeping a photo attached in a reopened reviewer composer meanwhile", async () => {
	const JPEG = "/9j/4AAQSkZJRgAB";
	const photo = (name: string) => ({ canceled: false, assets: [{ uri: `file:///${name}`, fileName: name, width: 1, height: 1, base64: JPEG, mimeType: "image/jpeg" }] });
	vi.mocked(launchImageLibraryAsync).mockResolvedValueOnce(photo("a.jpg") as never).mockResolvedValueOnce(photo("b.jpg") as never);
	let finish!: (id: string) => void;
	const p = { ...props(), attachmentKey: "reviewer.host.deferred", onSend: vi.fn(() => new Promise<string>((resolve) => { finish = resolve; })) };
	await act(async () => { renderer = create(<ReviewerComposer {...p} />); });
	await act(async () => renderer.root.findByType(ChatAttachmentMenu).props.onChoosePhoto());
	act(() => { renderer.root.findByProps({ accessibilityLabel: "Send reply" }).props.onPress(); });
	act(() => renderer.unmount());
	await act(async () => { renderer = create(<ReviewerComposer {...p} />); });
	await act(async () => renderer.root.findByType(ChatAttachmentMenu).props.onChoosePhoto());
	await act(async () => finish("send-id"));
	expect(renderer.root.findAllByProps({ accessibilityLabel: "Remove a.jpg" })).toHaveLength(0);
	expect(renderer.root.findAllByProps({ accessibilityLabel: "Remove b.jpg" })).toHaveLength(1);
});
