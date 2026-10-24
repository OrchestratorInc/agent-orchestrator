import type { CloudClient, Session, SessionPage } from "@aoagents/cloud-client";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const native = vi.hoisted(() => ({ alert: vi.fn(), platform: { OS: "ios" } }));
vi.mock("react-native", async () => {
	const React = await import("react");
	return {
		ActivityIndicator: "ActivityIndicator", Text: "Text", View: "View",
		Alert: { alert: native.alert },
		Platform: native.platform,
		StyleSheet: { create: (styles: object) => styles, hairlineWidth: 1 },
		useWindowDimensions: () => ({ width: 390, height: 844 }),
		FlatList: ({ data, renderItem, ListEmptyComponent, ItemSeparatorComponent, ...props }: {
			data: Session[]; renderItem(info: { item: Session }): React.ReactNode;
			ListEmptyComponent?: React.ReactNode; ItemSeparatorComponent?: React.ComponentType;
		}) => React.createElement("FlatList", { ...props, data }, data.length
			? data.map((item, index) => React.createElement(React.Fragment, { key: item.id }, index > 0 && ItemSeparatorComponent ? React.createElement(ItemSeparatorComponent) : null, renderItem({ item })))
			: ListEmptyComponent),
	};
});
vi.mock("../ThemeProvider", async () => {
	const { darkTheme } = await import("../theme");
	return { useTheme: () => darkTheme, useThemedStyles: (factory: (theme: typeof darkTheme) => unknown) => factory(darkTheme) };
});
vi.mock("../AgentLogo", () => ({ AgentLogo: "AgentLogo" }));
vi.mock("../ui", () => ({ Button: "Button", HeaderIconButton: "HeaderIconButton" }));

import { CloudSessionManager, type CloudSessionManagerProps } from "./CloudSessionManager";

let renderer: ReactTestRenderer | undefined;
const listSessions = vi.fn<CloudClient["listSessions"]>();
const deleteSession = vi.fn<CloudClient["deleteSession"]>();
const client = { listSessions, deleteSession } as unknown as CloudClient;
const session = (overrides: Partial<Session> = {}): Session => ({
	id: "worker-1", orgId: "org-1", projectId: "project-1", kind: "worker", harness: "codex",
	displayName: "Fix login", autoReviewEnabled: false, autoInjectCI: false, autoInjectReview: false,
	terminateOnPrMerge: false, branch: "fix-login", mode: "trusted", deniedCommands: [], interfaceMode: "chat",
	activityState: "idle", status: "idle", runtimeConnected: true, isTerminated: false,
	createdAt: "2026-10-20T00:00:00Z", updatedAt: "2026-10-20T00:00:00Z", ...overrides,
});
const page = (items: Session[], nextCursor?: string): SessionPage => ({ items, page: { hasMore: !!nextCursor, ...(nextCursor ? { nextCursor } : {}) } });
function deferred<T>() {
	let resolve!: (value: T) => void;
	let reject!: (reason: unknown) => void;
	const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail; });
	return { promise, resolve, reject };
}
const current = () => true;
const props = (overrides: Partial<CloudSessionManagerProps> = {}): CloudSessionManagerProps => ({
	client, orgId: "org-1", scopeKey: "account-1/org-1", isCurrent: current,
	projectNames: { "project-1": "Alpha", "project-2": "Beta" }, onBack: vi.fn(), ...overrides,
});
async function mount(overrides: Partial<CloudSessionManagerProps> = {}) {
	await act(async () => { renderer = create(<CloudSessionManager {...props(overrides)} />); });
	return renderer!.root;
}
const text = () => renderer!.root.findAllByType("Text" as never).map(node => node.props.children).flat().join(" ");
const buttons = (title: string) => renderer!.root.findAllByType("Button" as never).filter(node => node.props.title === title);
const press = async (title: string, index = 0) => act(async () => { buttons(title)[index].props.onPress(); });
function confirm() {
	const actions = native.alert.mock.calls.at(-1)![2] as { text: string; onPress?(): void }[];
	return actions.find(action => action.text === "Archive")!.onPress!();
}

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	vi.clearAllMocks();
	native.platform.OS = "ios";
	listSessions.mockReset().mockResolvedValue(page([session()]));
	deleteSession.mockReset().mockResolvedValue({ session: { id: "worker-1", desiredState: "deleted" } });
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
});
afterEach(async () => {
	if (renderer) await act(async () => renderer?.unmount());
	renderer = undefined;
	vi.restoreAllMocks(); vi.unstubAllGlobals();
});

it("lists every organization page with project and role identity, without archiving on arrival", async () => {
	listSessions.mockResolvedValueOnce(page([session()], "second")).mockResolvedValueOnce(page([
		session({ id: "orchestrator-2", projectId: "project-2", kind: "orchestrator", displayName: "", harness: "claude-code", desiredState: "paused", observedState: "stopped", runtimeConnected: false }),
	]));
	const root = await mount();
	expect(text()).toContain("Fix login");
	expect(text()).toContain("Claude Code");
	expect(text()).toContain("Alpha");
	expect(text()).toContain("Beta");
	expect(text()).toContain("Worker");
	expect(text()).toContain("Orchestrator");
	expect(text()).toContain("Paused");
	expect(text()).toContain("Paused sessions still count toward the Cloud session limit.");
	expect(listSessions.mock.calls.map(([orgId, options]) => [orgId, options?.cursor, options?.projectId])).toEqual([
		["org-1", undefined, undefined], ["org-1", "second", undefined],
	]);
	expect(listSessions.mock.calls.every(([, options]) => options?.signal instanceof AbortSignal)).toBe(true);
	const list = root.findByType("FlatList" as never);
	expect(list.props.keyExtractor(session())).toBe("worker-1");
	expect(list.props.style).toMatchObject({ flex: 1, minHeight: 0 });
	expect(deleteSession).not.toHaveBeenCalled();
});

it("bounds the Android dynamic sheet list and keeps scroll gestures inside it", async () => {
	native.platform.OS = "android";
	const root = await mount();
	const list = root.findByType("FlatList" as never);
	expect(list.props.style.height).toBeGreaterThan(0);
	expect(list.props.style.height).toBeLessThan(844);
	expect(list.props.nestedScrollEnabled).toBe(true);
});

it("requires confirmation naming the session and explains stopping it while retaining the conversation", async () => {
	await mount();
	expect(buttons("Archive")[0].props.accessibilityLabel).toBe("Archive Fix login");
	await press("Archive");
	expect(native.alert).toHaveBeenCalledWith("Archive “Fix login”?", expect.stringMatching(/stops the agent.*retains.*conversation/i), expect.any(Array));
	expect(deleteSession).not.toHaveBeenCalled();
	const actions = native.alert.mock.calls[0][2] as { text: string; style: string; onPress?(): void }[];
	expect(actions.find(action => action.text === "Cancel")?.style).toBe("cancel");
	expect(actions.find(action => action.text === "Archive")?.style).toBe("destructive");
});

it("archives only the confirmed session, refreshes observed state, and never claims capacity is free", async () => {
	const deletion = deferred<Awaited<ReturnType<CloudClient["deleteSession"]>>>();
	const onChanged = vi.fn();
	deleteSession.mockReturnValue(deletion.promise);
	await mount({ onChanged });
	await press("Archive");
	await act(async () => { confirm(); });
	expect(deleteSession).toHaveBeenCalledWith("org-1", "worker-1", { signal: expect.any(AbortSignal) });
	expect(text()).toContain("Fix login");
	listSessions.mockResolvedValue(page([session({ desiredState: "deleted", observedState: "running", isTerminated: true })]));
	await act(async () => { deletion.resolve({ session: { id: "worker-1", desiredState: "deleted" } }); });
	expect(onChanged).toHaveBeenCalledOnce();
	expect(listSessions).toHaveBeenCalledTimes(2);
	expect(text()).toContain("Cleanup pending");
	expect(text()).toContain("Cloud cleanup may take a moment. Refresh before trying your task again.");
	expect(text()).not.toMatch(/capacity freed|space available|\d+ \/ \d+/i);
	expect(buttons("Archive")).toHaveLength(0);
});

it("does not offer archive or restore for already archived sessions", async () => {
	listSessions.mockResolvedValue(page([session({ isTerminated: true, status: "terminated", desiredState: "deleted", observedState: "deleted" })]));
	await mount();
	expect(text()).toContain("Archived");
	expect(buttons("Archive")).toHaveLength(0);
	expect(buttons("Restore")).toHaveLength(0);
	expect(deleteSession).not.toHaveBeenCalled();
});

it("keeps a load failure inline and retries without changing any session", async () => {
	listSessions.mockRejectedValueOnce(new Error("Network unavailable"));
	await mount();
	expect(text()).toContain("Network unavailable");
	expect(text()).not.toContain("No Cloud sessions");
	await press("Retry");
	expect(text()).toContain("Fix login");
	expect(text()).not.toContain("Network unavailable");
	expect(deleteSession).not.toHaveBeenCalled();
});

it("keeps the list and archive failure visible, with retry requiring fresh confirmation", async () => {
	deleteSession.mockRejectedValueOnce(new Error("Archive unavailable"));
	await mount();
	await press("Archive");
	await act(async () => { confirm(); });
	expect(text()).toContain("Fix login");
	expect(text()).toContain("Archive unavailable");
	expect(listSessions).toHaveBeenCalledOnce();
	await press("Archive");
	expect(native.alert).toHaveBeenCalledTimes(2);
	expect(deleteSession).toHaveBeenCalledOnce();
});

it("refreshes independently and preserves current rows while a refresh fails", async () => {
	await mount();
	listSessions.mockRejectedValueOnce(new Error("Refresh unavailable"));
	await press("Refresh");
	expect(text()).toContain("Fix login");
	expect(text()).toContain("Refresh unavailable");
	expect(deleteSession).not.toHaveBeenCalled();
});

it("returns to the draft and invalidates a confirmation without archiving", async () => {
	const onBack = vi.fn();
	await mount({ onBack });
	await press("Archive");
	await act(async () => renderer!.root.findByType("HeaderIconButton" as never).props.onPress());
	expect(onBack).toHaveBeenCalledOnce();
	await act(async () => { confirm(); });
	expect(deleteSession).not.toHaveBeenCalled();
});

it("ignores an old owner's pending list result and cancels its request when the scope changes", async () => {
	const oldPage = deferred<SessionPage>();
	listSessions.mockReturnValueOnce(oldPage.promise);
	await mount();
	const oldSignal = listSessions.mock.calls[0][1]!.signal!;
	listSessions.mockResolvedValueOnce(page([session({ id: "new-worker", displayName: "Current account task", orgId: "org-2" })]));
	await act(async () => { renderer!.update(<CloudSessionManager {...props({ orgId: "org-2", scopeKey: "account-2/org-2" })} />); });
	expect(oldSignal.aborted).toBe(true);
	await act(async () => { oldPage.resolve(page([session({ displayName: "Old account task" })])); });
	expect(text()).toContain("Current account task");
	expect(text()).not.toContain("Old account task");
});

it("blocks a saved confirmation and refresh when the account expires", async () => {
	let isCurrent = true;
	const stillCurrent = () => isCurrent;
	await mount({ isCurrent: stillCurrent });
	await press("Archive");
	isCurrent = false;
	await act(async () => { confirm(); });
	expect(deleteSession).not.toHaveBeenCalled();
	await press("Refresh");
	expect(listSessions).toHaveBeenCalledOnce();
});

it("does not request the next page after the account expires", async () => {
	let isCurrent = true;
	const pending = deferred<SessionPage>();
	listSessions.mockReturnValueOnce(pending.promise);
	await mount({ isCurrent: () => isCurrent });
	isCurrent = false;
	await act(async () => { pending.resolve(page([session()], "second")); });
	expect(listSessions).toHaveBeenCalledOnce();
	expect(text()).not.toContain("Fix login");
});

it("rejects an incomplete or looping page cursor and leaves retry available", async () => {
	listSessions.mockResolvedValue(page([session()], "same-page"));
	await mount();
	expect(listSessions).toHaveBeenCalledTimes(2);
	expect(text()).toContain("Cloud could not load all sessions");
	expect(buttons("Retry")).toHaveLength(1);
	expect(text()).not.toContain("No Cloud sessions");
});

it("deduplicates page overlap by session identity", async () => {
	listSessions.mockResolvedValueOnce(page([session()], "second")).mockResolvedValueOnce(page([session({ displayName: "Updated task" })]));
	const root = await mount();
	expect(root.findByType("FlatList" as never).props.data).toHaveLength(1);
	expect(text()).toContain("Updated task");
	expect(text()).not.toContain("Fix login");
});

it("blocks confirmation if refresh shows the session was already archived elsewhere", async () => {
	await mount();
	await press("Archive");
	listSessions.mockResolvedValue(page([session({ isTerminated: true, status: "terminated" })]));
	await press("Refresh");
	await act(async () => { confirm(); });
	expect(deleteSession).not.toHaveBeenCalled();
	expect(text()).toContain("Archived");
});

it("invalidates the old owner's confirmation when switching organizations", async () => {
	await mount();
	await press("Archive");
	await act(async () => { renderer!.update(<CloudSessionManager {...props({ orgId: "org-2", scopeKey: "account-1/org-2" })} />); });
	await act(async () => { confirm(); });
	expect(deleteSession).not.toHaveBeenCalled();
});

it("ignores an old owner's archive success after changing scope", async () => {
	const deletion = deferred<Awaited<ReturnType<CloudClient["deleteSession"]>>>();
	const onChanged = vi.fn();
	deleteSession.mockReturnValue(deletion.promise);
	await mount({ onChanged });
	await press("Archive");
	await act(async () => { confirm(); });
	const signal = deleteSession.mock.calls[0][2]!.signal!;
	await act(async () => { renderer!.update(<CloudSessionManager {...props({ orgId: "org-2", scopeKey: "account-1/org-2", onChanged })} />); });
	expect(signal.aborted).toBe(true);
	await act(async () => { deletion.resolve({ session: { id: "worker-1", desiredState: "deleted" } }); });
	expect(onChanged).not.toHaveBeenCalled();
	expect(listSessions).toHaveBeenCalledTimes(2);
	expect(text()).not.toContain("Cloud cleanup may take a moment");
});

it("shows a completed empty response without offering unrelated actions", async () => {
	listSessions.mockResolvedValue(page([]));
	await mount();
	expect(text()).toContain("No Cloud sessions.");
	expect(buttons("Archive")).toHaveLength(0);
	expect(buttons("Retry")).toHaveLength(0);
	expect(deleteSession).not.toHaveBeenCalled();
});

it("aborts all work on unmount and ignores a late archive result", async () => {
	const deletion = deferred<Awaited<ReturnType<CloudClient["deleteSession"]>>>();
	const onChanged = vi.fn();
	deleteSession.mockReturnValue(deletion.promise);
	await mount({ onChanged });
	await press("Archive");
	await act(async () => { confirm(); });
	const signal = deleteSession.mock.calls[0][2]!.signal!;
	await act(async () => renderer!.unmount());
	renderer = undefined;
	expect(signal.aborted).toBe(true);
	await act(async () => { deletion.resolve({ session: { id: "worker-1", desiredState: "deleted" } }); });
	expect(onChanged).not.toHaveBeenCalled();
	expect(listSessions).toHaveBeenCalledOnce();
});
