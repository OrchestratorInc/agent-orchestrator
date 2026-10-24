import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ConversationSnapshot } from "./types";

const state = vi.hoisted(() => ({ sheetKey: "", refresh: vi.fn(), settings: vi.fn(async () => {}) }));
vi.mock("expo-router", () => ({ useLocalSearchParams: () => ({ sheetKey: state.sheetKey }), useRouter: () => ({ back: vi.fn() }) }));
vi.mock("react-native", () => ({ ActivityIndicator: "ActivityIndicator", Pressable: "Pressable", ScrollView: "ScrollView", StyleSheet: { create: (styles: unknown) => styles }, Switch: "Switch", Text: "Text", View: "View" }));
vi.mock("../icons", () => ({ Feather: "Feather" }));
vi.mock("../haptics", () => ({ haptics: { tap: vi.fn(), select: vi.fn() } }));
vi.mock("../ThemeProvider", () => ({ useTheme: () => ({}), useThemedStyles: (factory: (theme: object) => unknown) => factory({}) }));
vi.mock("../ui", async () => {
	const { createElement } = await import("react");
	return { SheetHeader: ({ right }: { right?: import("react").ReactNode }) => createElement("View", null, right) };
});
vi.mock("../backNavigation", () => ({ backOr: vi.fn() }));
vi.mock("../RouteErrorBoundary", () => ({ SheetErrorBoundary: "SheetErrorBoundary" }));

import ChatSettingsRoute from "../../app/sheets/chat-settings";
import { parkChatSheet, resetChatSheets } from "./chatSheetRegistry";

const models = [
	{ id: "old", displayName: "Old model", default: true, efforts: ["low", "high"] },
	{ id: "native", displayName: "Native model", default: false, efforts: ["low", "high"] },
];
const snapshot: ConversationSnapshot = {
	conversationId: "c", sessionId: "s", harness: "claude-code", mode: "chat", controller: { state: "ready" },
	latestSequence: 0, oldestSequence: 0, hasMoreBefore: false, turns: [], items: [], settings: { model: "old", reasoningEffort: "low" },
};
let rendered: ReactTestRenderer | undefined;
beforeEach(() => { vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true); vi.spyOn(console, "error").mockImplementation(() => {}); resetChatSheets(); state.refresh.mockReset(); state.settings.mockClear(); });
afterEach(() => { if (rendered) act(() => rendered!.unmount()); rendered = undefined; vi.restoreAllMocks(); vi.unstubAllGlobals(); });
async function mount() {
	state.sheetKey = parkChatSheet({ kind: "turn-settings", snapshot, models, options: [], onSettings: state.settings, onOption: async () => [], onRefresh: state.refresh });
	await act(async () => { rendered = create(<ChatSettingsRoute />); });
}
const choice = (label: string) => rendered!.root.findAllByProps({ accessibilityRole: "radio" }).find((node) => node.findAllByProps({ children: label }).length)!;
async function refresh() { await act(async () => rendered!.root.findByProps({ accessibilityLabel: "Refresh turn settings" }).props.onPress()); }

it("refreshes an open Cloud sheet's selected native model and subsequent settings writes", async () => {
	state.refresh.mockResolvedValue({ models, configOptions: [], settings: { model: "native", reasoningEffort: "high", executionMode: "plan" } });
	await mount(); await refresh();
	expect(choice("Native model").props.accessibilityState.selected).toBe(true);
	expect(choice("Old model").props.accessibilityState.selected).toBe(false);
	await act(async () => choice("Low").props.onPress());
	expect(state.settings).toHaveBeenCalledExactlyOnceWith({ model: "native", reasoningEffort: "low", executionMode: "plan" });
});

it("preserves a local sheet's settings when its refreshed catalog has no Cloud settings", async () => {
	state.refresh.mockResolvedValue({ models, configOptions: [] });
	await mount(); await refresh();
	expect(choice("Old model").props.accessibilityState.selected).toBe(true);
	expect(choice("Native model").props.accessibilityState.selected).toBe(false);
	expect(state.settings).not.toHaveBeenCalled();
});

it("preserves current selections when refreshing controls fails", async () => {
	state.refresh.mockRejectedValue(new Error("Worker unavailable"));
	await mount(); await refresh();
	expect(choice("Old model").props.accessibilityState.selected).toBe(true);
	expect(rendered!.root.findAllByProps({ children: "Worker unavailable" }).length).toBeGreaterThan(0);
	expect(state.settings).not.toHaveBeenCalled();
});
