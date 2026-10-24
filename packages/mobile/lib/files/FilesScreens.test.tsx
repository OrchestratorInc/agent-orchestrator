import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { WorkspaceFileDetail } from "./api";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });
const state = vi.hoisted(() => ({ params: {} as Record<string, any>, scope: {} as Record<string, any>, manifest: {} as Record<string, any>, push: vi.fn(), setParams: vi.fn(), localRead: vi.fn(), imageSource: vi.fn() }));
vi.mock("expo-router", () => ({ useLocalSearchParams: () => state.params, useNavigation: () => ({ setOptions: vi.fn() }), useRouter: () => ({ push: state.push, setParams: state.setParams, navigate: vi.fn() }) }));
vi.mock("react-native", async () => {
	const { createElement } = await import("react");
	const list = (name: string) => (props: Record<string, any>) => createElement(name, props, props.ListHeaderComponent);
	return {
		ActivityIndicator: "ActivityIndicator", Pressable: "Pressable", RefreshControl: "RefreshControl", FlatList: list("FlatList"), SectionList: list("SectionList"), Text: "Text", TextInput: "TextInput", View: "View", ScrollView: "ScrollView", Image: "Image", Platform: { OS: "ios" },
		AccessibilityInfo: { announceForAccessibility: vi.fn() }, useWindowDimensions: () => ({ width: 390 }), StyleSheet: { create: (styles: unknown) => styles, hairlineWidth: 1 },
	};
});
vi.mock("react-native-gesture-handler", () => ({ FlatList: "GestureFlatList", GestureDetector: "GestureDetector" }));
vi.mock("react-native-svg", () => ({ SvgXml: "SvgXml" }));
vi.mock("expo-clipboard", () => ({ setStringAsync: vi.fn() }));
vi.mock("../store", () => ({ useApp: () => ({ config: null, currentHostId: "active-mac", connection: "closed", loading: false }), HostScope: ({ children }: { children: unknown }) => children }));
vi.mock("./FilesScope", () => ({ FilesScope: ({ children }: { children: unknown }) => children, useFilesScope: () => state.scope }));
vi.mock("../ThemeProvider", () => ({ useTheme: () => ({}), useThemedStyles: (factory: (theme: object) => unknown) => factory({}) }));
vi.mock("../icons", () => ({ Feather: "Feather" }));
vi.mock("../haptics", () => ({ haptics: { tap: vi.fn(), select: vi.fn(), success: vi.fn(), error: vi.fn() } }));
vi.mock("../ui", () => ({ Button: "Button", EmptyState: "EmptyState" }));
vi.mock("../PickerOptions", () => ({ PickerOptions: "PickerOptions" }));
vi.mock("../RouteErrorBoundary", () => ({ RouteErrorBoundary: "RouteErrorBoundary" }));
vi.mock("../chat/ChatMarkdown", () => ({ ChatMarkdown: "ChatMarkdown" }));
vi.mock("./components", () => ({
	ChangeRow: "ChangeRow", FileTypeIcon: "FileTypeIcon", FolderTypeIcon: "FolderTypeIcon", LineCounts: "LineCounts", ReviewTray: "ReviewTray", SegmentedTabs: "SegmentedTabs", DiffLine: "DiffLine", FoldRow: "FoldRow", InlineComment: "InlineComment", SelectionComposer: "SelectionComposer", codeContentWidth: () => 390, gutterWidth: () => 30,
}));
vi.mock("./useLineDrag", () => ({ useLineDrag: () => ({ reset: vi.fn(), gesture: {}, listProps: {}, dragging: false }) }));
vi.mock("./useWorkspace", () => ({ useWorkspaceManifest: () => ({ manifest: state.manifest, loading: false, refreshing: false, refresh: vi.fn() }) }));
vi.mock("./useReviewSender", () => ({ useReviewSender: () => ({ all: [], comments: [], sending: false, send: vi.fn(), remove: vi.fn() }) }));
vi.mock("./api", () => ({ getWorkspaceFile: state.localRead, getWorkspaceTree: state.localRead, searchWorkspaceFiles: state.localRead, workspaceImageSource: state.imageSource, sendFileFeedback: vi.fn() }));

import SessionFilesScreen from "../../app/files/[sessionId]/index";
import SessionFileScreen from "../../app/files/[sessionId]/file";
import { clearCloudReviewsForAccount, cloudReviewKey, reviewComments } from "./reviewStore";

let screen: ReactTestRenderer;
const binary: WorkspaceFileDetail = { path: "picture.png", status: "modified", additions: 0, deletions: 0, binary: true, deleted: false, size: 12, content: "", contentTruncated: false, diff: "", diffTruncated: false, fileFingerprint: "hash", workspaceVersion: "version" };
beforeEach(() => {
	state.manifest = { files: [], sections: { committed: [], staged: [], unstaged: [], untracked: [] }, summary: { files: 0, additions: 0, deletions: 0 }, workspaceVersion: "version" };
	state.params = { sessionId: "worker", path: "picture.png", source: "cloud", sourceId: "org", scopeKey: "lease" };
	state.scope = {
		sessionId: "worker", source: { kind: "cloud", id: "org" }, owner: { source: { kind: "cloud", id: "org" }, scopeKey: "lease" }, reviewKey: "cloud-review",
		workspace: { scopeKey: "lease", isCurrent: () => true, getTree: vi.fn(async () => ({ path: "", entries: [{ name: "README.md", path: "README.md", type: "file", status: "unmodified" }], truncated: false })), getFile: vi.fn(async () => binary), search: vi.fn(async () => ({ results: [] })), sendFeedback: vi.fn(async () => {}) },
	};
	vi.clearAllMocks();
});
afterEach(() => { if (screen) act(() => screen.unmount()); vi.useRealTimers(); });

it("uses the Cloud browser rather than the active paired daemon and preserves ownership when opening a file", async () => {
	await act(async () => { screen = create(<SessionFilesScreen />); });
	await act(async () => screen.root.findByType("SegmentedTabs" as never).props.onChange("all"));
	const list = screen.root.findByType("FlatList" as never);
	expect(list.props.data.map((item: { path: string }) => item.path)).toEqual(["README.md"]);
	const row = list.props.renderItem({ item: list.props.data[0] });
	await act(async () => row.props.onPress());
	expect(state.push).toHaveBeenCalledWith({ pathname: "/files/[sessionId]/file", params: { sessionId: "worker", path: "README.md", mode: "file", previousPath: undefined, source: "cloud", sourceId: "org", scopeKey: "lease" } });
	expect(state.localRead).not.toHaveBeenCalled();
});

it("shows Cloud binary fallback without constructing a Local authenticated image URL", async () => {
	await act(async () => { screen = create(<SessionFileScreen />); });
	expect(screen.root.findByType("EmptyState" as never).props.title).toBe("Binary file");
	expect(screen.root.findAllByType("Image" as never)).toHaveLength(0);
	expect(state.imageSource).not.toHaveBeenCalled();
	expect(state.localRead).not.toHaveBeenCalled();
});

it("preserves before and after image previews for the owning Local source", async () => {
	state.scope.source = { kind: "local", id: "paired-mac" };
	state.scope.workspace.imageSource = (_id: string, _path: string, side: string) => ({ uri: `https://paired-mac/${side}`, headers: { Authorization: "Bearer paired-only" } });
	await act(async () => { screen = create(<SessionFileScreen />); });
	expect(screen.root.findAllByType("Image" as never).map((image) => image.props.source)).toEqual([
		{ uri: "https://paired-mac/before", headers: { Authorization: "Bearer paired-only" } },
		{ uri: "https://paired-mac/after", headers: { Authorization: "Bearer paired-only" } },
	]);
});

it("presents a failed Cloud search as a failure rather than an empty repository", async () => {
	vi.useFakeTimers();
	state.scope.workspace.search = async () => { throw new Error("Workspace is paused"); };
	await act(async () => { screen = create(<SessionFilesScreen />); });
	await act(async () => screen.root.findByType("SegmentedTabs" as never).props.onChange("all"));
	act(() => screen.root.findByType("TextInput" as never).props.onChangeText("readme"));
	await act(async () => { await vi.advanceTimersByTimeAsync(250); });
	const empty = screen.root.findByType("FlatList" as never).props.ListEmptyComponent;
	expect(empty.props.title).toBe("Couldn't search files");
	expect(empty.props.message).toBe("Workspace is paused");
});

it("ignores a late file read after the reader has moved to another file", async () => {
	let finishOld!: (detail: WorkspaceFileDetail) => void;
	state.scope.workspace.getFile = (_id: string, path: string) => path === "picture.png"
		? new Promise<WorkspaceFileDetail>((resolve) => { finishOld = resolve; })
		: Promise.resolve({ ...binary, path, binary: false, content: "# Current file\n", status: "unmodified" });
	await act(async () => { screen = create(<SessionFileScreen />); });
	state.params.path = "README.md";
	await act(async () => screen.update(<SessionFileScreen />));
	await act(async () => finishOld(binary));
	expect(screen.root.findByType("ChatMarkdown" as never).props.text).toBe("# Current file\n");
	expect(screen.root.findAllByType("EmptyState" as never)).toHaveLength(0);
});

it("offers only checked-out repositories and preserves the selected Files tab when changing repository", async () => {
	state.manifest.repositories = [{ name: "main", label: "Main repository", main: true, present: true }, { name: "shared-api", present: true }, { name: "not-checked-out", present: false }];
	await act(async () => { screen = create(<SessionFilesScreen />); });
	await act(async () => screen.root.findByType("SegmentedTabs" as never).props.onChange("all"));
	await act(async () => screen.root.findByProps({ accessibilityLabel: "Choose repository" }).props.onPress());
	const picker = screen.root.findByType("PickerOptions" as never);
	expect(picker.props.options.map((option: { id: string }) => option.id)).toEqual(["", "shared-api"]);
	await act(async () => picker.props.onSelect("shared-api"));
	expect(state.setParams).toHaveBeenCalledWith({ repository: "shared-api", tab: "all" });
	state.scope.owner = { ...state.scope.owner, repository: "shared-api" };
	await act(async () => screen.update(<SessionFilesScreen />));
	act(() => screen.root.findByProps({ accessibilityLabel: "Choose repository" }).props.onPress());
	const siblingPicker = screen.root.findByType("PickerOptions" as never);
	expect(siblingPicker.props.selectedValue).toBe("shared-api");
	act(() => siblingPicker.props.onSelect(""));
	expect(state.setParams).toHaveBeenLastCalledWith({ repository: "", tab: "all" });
});

function selectFirstLine() {
	const list = screen.root.findByType("GestureFlatList" as never);
	const line = list.props.renderItem({ item: list.props.data[0], index: 0 });
	line.props.onSelect(0);
}
function textWorkspace() {
	state.params.path = "src/first.ts";
	state.scope.workspace.getFile = vi.fn(async (_id: string, path: string) => ({ ...binary, path, binary: false, status: "unmodified", content: "first line\nsecond line\n" }));
}

it("does not submit the same immediate feedback twice before the busy state renders", async () => {
	textWorkspace();
	let finish!: () => void;
	state.scope.workspace.sendFeedback = vi.fn(() => new Promise<void>((resolve) => { finish = resolve; }));
	await act(async () => { screen = create(<SessionFileScreen />); });
	act(selectFirstLine);
	const send = screen.root.findByType("SelectionComposer" as never).props.onAction;
	act(() => { send("send", "Please explain"); send("send", "Please explain"); });
	expect(state.scope.workspace.sendFeedback).toHaveBeenCalledTimes(1);
	await act(async () => finish());
});

it("does not clear a new file selection when feedback for the previous file finishes", async () => {
	textWorkspace();
	let finish!: () => void;
	state.scope.workspace.sendFeedback = () => new Promise<void>((resolve) => { finish = resolve; });
	await act(async () => { screen = create(<SessionFileScreen />); });
	act(selectFirstLine);
	act(() => screen.root.findByType("SelectionComposer" as never).props.onAction("send", "Ask about first.ts"));
	state.params.path = "src/next.ts";
	await act(async () => screen.update(<SessionFileScreen />));
	act(selectFirstLine);
	await act(async () => finish());
	expect(screen.root.findByType("SelectionComposer" as never).props.label).toContain("next.ts");
});

it("does not clear a new selection after leaving and returning to the file of an older pending send", async () => {
	textWorkspace();
	state.scope.reviewKey = "aba-feedback";
	let finish!: () => void;
	state.scope.workspace.sendFeedback = () => new Promise<void>((resolve) => { finish = resolve; });
	await act(async () => { screen = create(<SessionFileScreen />); });
	act(selectFirstLine);
	act(() => screen.root.findByType("SelectionComposer" as never).props.onAction("send", "Accepted original file feedback"));
	state.params.path = "src/next.ts";
	await act(async () => screen.update(<SessionFileScreen />));
	state.params.path = "src/first.ts";
	await act(async () => screen.update(<SessionFileScreen />));
	act(selectFirstLine);
	await act(async () => finish());
	expect(screen.root.findByType("SelectionComposer" as never).props.label).toContain("first.ts");
	expect(reviewComments("aba-feedback").map((comment) => ({ feedback: comment.feedback, status: comment.status }))).toContainEqual({ feedback: "Accepted original file feedback", status: "sent" });
});

it("keeps a replacement selection on the same file when older feedback finishes", async () => {
	textWorkspace();
	let finish!: () => void;
	state.scope.workspace.sendFeedback = () => new Promise<void>((resolve) => { finish = resolve; });
	await act(async () => { screen = create(<SessionFileScreen />); });
	act(selectFirstLine);
	act(() => screen.root.findByType("SelectionComposer" as never).props.onAction("send", "Original selection"));
	act(() => screen.root.findByType("SelectionComposer" as never).props.onClose());
	act(selectFirstLine);
	await act(async () => finish());
	expect(screen.root.findByType("SelectionComposer" as never).props.busy).toBe(false);
});

it("does not resurrect retired Cloud review comments when a late feedback acknowledgment arrives", async () => {
	textWorkspace();
	state.scope.reviewKey = cloudReviewKey('["https://api.aoagents.dev","account","org"]', "worker-retired");
	let finish!: () => void;
	state.scope.workspace.sendFeedback = () => new Promise<void>((resolve) => { finish = resolve; });
	await act(async () => { screen = create(<SessionFileScreen />); });
	act(selectFirstLine);
	act(() => screen.root.findByType("SelectionComposer" as never).props.onAction("send", "Explain this"));
	act(() => clearCloudReviewsForAccount("https://api.aoagents.dev", "account"));
	await act(async () => finish());
	expect(reviewComments(state.scope.reviewKey)).toHaveLength(0);
});
