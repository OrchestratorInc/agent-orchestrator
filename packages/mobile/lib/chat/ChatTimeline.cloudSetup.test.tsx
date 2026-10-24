import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, expect, it, vi } from "vitest";
import type { ClientEvent } from "@aoagents/cloud-client";
import type { ConversationSnapshot } from "./types";
import type { ReactNode } from "react";
import type { ServerConfig } from "../config";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });
vi.mock("react-native", async () => {
	const { createElement } = await import("react");
	return {
		ActivityIndicator: "ActivityIndicator", Image: "Image", Modal: "Modal", Pressable: "Pressable", ScrollView: "ScrollView", Switch: "Switch", Text: "Text", TextInput: "TextInput", View: "View",
		Platform: { OS: "ios" }, useWindowDimensions: () => ({ width: 390, height: 844 }),
		StyleSheet: { create: (styles: unknown) => styles, hairlineWidth: 1 },
		FlatList: ({ data, renderItem }: { data: object[]; renderItem: (value: object) => ReactNode }) => createElement("FlatList", null, ...data.map((item, index) => createElement("Row", { key: index }, renderItem({ item })))),
	};
});
vi.mock("expo-clipboard", () => ({ setStringAsync: vi.fn() }));
vi.mock("expo-linking", () => ({ openURL: vi.fn() }));
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ top: 0, bottom: 0 }) }));
vi.mock("../config", () => ({ authHeaders: (config: ServerConfig) => ({ Authorization: `Bearer ${config.password}`, "X-AO-Expected-Host-ID": config.hostId }), httpBase: (config: ServerConfig) => `http://${config.host}:${config.httpPort}` }));
vi.mock("../api", () => ({ previewFilePath: (session: string, path: string) => `/sessions/${session}/files/${path}` }));
vi.mock("../icons", () => ({ Feather: "Feather" }));
vi.mock("../haptics", () => ({ haptics: { tap: vi.fn(), success: vi.fn() } }));
vi.mock("../store", () => ({ useApp: () => ({ config: { host: "active-a", httpPort: "3011", password: "secret-a", hostId: "host-a" } }) }));
vi.mock("../ThemeProvider", () => ({ useTheme: () => ({}), useThemedStyles: (factory: (theme: object) => unknown) => factory({}) }));
vi.mock("../ui", () => ({ MascotLamp: "MascotLamp", Spinning: "Spinning" }));
vi.mock("./TurnFoldMotion", () => ({ FoldBody: "FoldBody", FoldChevron: "FoldChevron", FoldLayout: "FoldLayout", StatusLabel: "StatusLabel", StatusSpinner: "StatusSpinner" }));
vi.mock("./ChatMarkdown", () => ({ ChatMarkdown: "ChatMarkdown" }));
vi.mock("./HighlightedCodeText", () => ({ HighlightedCodeText: "HighlightedCodeText" }));
vi.mock("./elicitation-native-controls", () => ({ ElicitationAction: "ElicitationAction", ElicitationChoiceList: "ElicitationChoiceList", ElicitationTextField: "ElicitationTextField" }));
vi.mock("../files/navigation", () => ({ useFilesNavigation: () => ({}) }));
vi.mock("./SentResponseQuotes", () => ({ SentResponseQuotes: "SentResponseQuotes" }));
vi.mock("./SessionStartup", () => ({ SessionStartupStatus: "SessionStartupStatus" }));
vi.mock("./RenderFrame", () => ({ RenderFrame: "RenderFrame" }));

import { ChatTimeline } from "./ChatTimeline";
import { toConversationItems } from "../cloud/events";

let screen: ReactTestRenderer;
afterEach(() => { if (screen) act(() => screen.unmount()); });
const setup = (payload: object): ClientEvent => ({ sessionId: "cloud-session", sequence: 8, type: "workspace.setup", payload, createdAt: "2026-10-21T00:00:00Z" } as unknown as ClientEvent);
const snapshot = (items: ConversationSnapshot["items"]): ConversationSnapshot => ({
	conversationId: "cloud-conversation", sessionId: "cloud-session", harness: "codex", mode: "chat", controller: { state: "ready" },
	latestSequence: 8, oldestSequence: 8, hasMoreBefore: false, turns: [], items, settings: {}, capabilities: [],
});
const mount = (items: ConversationSnapshot["items"]) => act(() => {
	screen = create(<ChatTimeline config={null} snapshot={snapshot(items)} loadingOlder={false} onLoadOlder={vi.fn()} approvalPending={false} inputPending={false} onDecide={vi.fn()} onResolveInput={vi.fn()} />);
});
const text = (value: string) => screen.root.findAllByType("Text" as never).filter((node) => [node.props.children].flat(Infinity).join("") === value).length;

it.each(["human", "steer"])("loads %s attachment previews only from the routed host, including memoized config updates", (kind) => {
	const body = "Inspect\n\nAttached files (read these files in the workspace):\n- .ao/attachments/image-test.png";
	const item = kind === "human"
		? { kind: "message", id: "m1", sequence: 1, revision: 1, role: "user", origin: "human", text: body, createdAt: "now" }
		: { kind: "activity", id: "a1", sequence: 1, revision: 1, activityKind: "system", status: "completed", summary: body, detail: { event: "steer", text: body }, createdAt: "now" };
	const data = snapshot([item] as ConversationSnapshot["items"]);
	const callbacks = { onLoadOlder: vi.fn(), onDecide: vi.fn(), onResolveInput: vi.fn() };
	const render = (config: ServerConfig | null) => <ChatTimeline config={config} snapshot={data} loadingOlder={false} approvalPending={false} inputPending={false} {...callbacks} />;
	const config = { host: "routed-b", httpPort: "3011", muxPort: "3011", password: "secret-b", hostId: "host-b" };
	act(() => { screen = create(render(config)); });
	const expected = { uri: "http://routed-b:3011/sessions/cloud-session/files/.ao/attachments/image-test.png", headers: { Authorization: "Bearer secret-b", "X-AO-Expected-Host-ID": "host-b" } };
	expect(screen.root.findAllByType("Image" as never).map(node => node.props.source)).toEqual([expected, expected]);
	act(() => screen.root.findByProps({ accessibilityLabel: "Open image-test.png" }).props.onPress());
	expect(screen.root.findByType("Modal" as never).props.visible).toBe(true);
	act(() => screen.update(render({ ...config, host: "new-b", password: "rotated-b" })));
	for (const image of screen.root.findAllByType("Image" as never)) expect(image.props.source).toMatchObject({ uri: expect.stringContaining("http://new-b:"), headers: { Authorization: "Bearer rotated-b", "X-AO-Expected-Host-ID": "host-b" } });
	act(() => screen.update(render(null)));
	expect(screen.root.findAllByType("Image" as never)).toHaveLength(0);
	expect(text("image-test.png")).toBeGreaterThan(0);
});

it("renders Setup success as its own row with duration and expandable safe output", () => {
	mount(toConversationItems([setup({ status: "succeeded", exitCode: 0, durationMs: 12_400, output: "\u001b[32mstack ready\u001b[0m", credentials: "private" })]));
	expect(text("Setup passed in 12s")).toBeGreaterThan(0);
	expect(text("stack ready")).toBe(0);
	const button = screen.root.findByProps({ accessibilityLabel: "Show Setup output" });
	expect(button.props.accessibilityState).toEqual({ expanded: false });
	act(() => button.props.onPress());
	expect(text("stack ready")).toBeGreaterThan(0);
	expect(text("private")).toBe(0);
	expect(screen.root.findByProps({ accessibilityLabel: "Hide Setup output" }).props.accessibilityState).toEqual({ expanded: true });
});

it.each([
	[{ status: "failed", step: 2, exitCode: 0, durationMs: 0 }, "Setup step 2 failed (exit 0)"],
	[{ status: "timed_out", step: 3, exitCode: -1, durationMs: 1_800_000 }, "Setup step 3 timed out"],
])("renders the Setup failure outcome without requiring output", (payload, headline) => {
	mount(toConversationItems([setup(payload)]));
	expect(text(headline)).toBeGreaterThan(0);
	expect(screen.root.findAllByProps({ accessibilityLabel: "Show Setup output" })).toHaveLength(0);
});

it("keeps ordinary local system activity on its existing row", () => {
	mount([{ kind: "activity", id: "local", sequence: 1, revision: 1, activityKind: "system", status: "completed", summary: "Session resumed", createdAt: "now" }]);
	expect(text("Session resumed")).toBeGreaterThan(0);
	expect(screen.root.findAllByProps({ accessibilityLabel: "Show Setup output" })).toHaveLength(0);
});
