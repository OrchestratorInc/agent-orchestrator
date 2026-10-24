import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DashboardSession } from "../api";
import type { CloudInterfaceStatus } from "../cloud/interfaceTransition";
import type { ScopedBoard, SourceRef } from "../environment/scopedBoard";

const fixture = vi.hoisted(() => ({
	params: {} as Record<string, string | undefined>,
	board: { projects: [], sessions: [], orchestrators: [], sources: {} } as ScopedBoard,
	cloudSource: { kind: "cloud", scopeKey: "cloud-account:org-1" },
	localSource: { kind: "local", scopeKey: "desktop-1" },
	status: undefined as CloudInterfaceStatus | undefined,
	sourceFor: vi.fn(), configForHost: vi.fn(), refreshSource: vi.fn(async () => {}),
	interfaceHook: vi.fn(), interfaceRefresh: vi.fn(async () => {}),
	interfaceStart: vi.fn(), interfaceCancel: vi.fn(), interfaceAcknowledge: vi.fn(),
	lookUpSession: vi.fn(), replace: vi.fn(), navigate: vi.fn(), push: vi.fn(),
	auth: { signedIn: true as boolean | null, busy: false, orgId: "org-1" as string | null, orgLoading: false, orgError: null as string | null },
	retryOrg: vi.fn(async () => {}), signIn: vi.fn(async () => true),
}));

vi.mock("expo-router", () => ({
	useLocalSearchParams: () => fixture.params,
	useRouter: () => ({ replace: fixture.replace, navigate: fixture.navigate, push: fixture.push }),
}));
vi.mock("react-native", async () => {
	const React = await import("react");
	return {
		ActivityIndicator: "ActivityIndicator", AppState: { currentState: "active" },
		StyleSheet: { create: (styles: object) => styles },
		View: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("View", props, children),
	};
});
vi.mock("../store", () => ({ useApp: () => ({
	scopedBoard: fixture.board, sourceFor: fixture.sourceFor, refreshSource: fixture.refreshSource,
	configForHost: fixture.configForHost, hostStates: [{ hostId: "desktop-1", connection: "open" }],
}) }));
vi.mock("../config", () => ({ machineIdentity: (config: { hostId: string }) => `host.${config.hostId}` }));
vi.mock("./sessionLookup", () => ({ lookUpSession: fixture.lookUpSession }));
vi.mock("../cloud/useCloudInterfaceTransition", () => ({ useCloudInterfaceTransition: fixture.interfaceHook }));
vi.mock("../cloud/useCloudSignInAction", () => ({ useCloudSignInAction: () => ({ busy: false, signIn: fixture.signIn }) }));
vi.mock("../cloud/authStore", () => ({ useCloudAuth: () => ({ ...fixture.auth, retryOrgResolution: fixture.retryOrg }) }));
// Retain a child-owned value across updates, as the real Chat discovery/resume
// refs and terminal buffers do. A source switch must replace this owner.
function sessionSurface(name: string, React: typeof import("react")) {
	return function SessionSurface(props: { source: SourceRef; session: { id: string } }) {
		const [openedWith] = React.useState(() => ({
			source: props.source,
			scopeKey: fixture.sourceFor(props.source)?.scopeKey,
			sessionId: props.session.id,
		}));
		return React.createElement(name, { ...props, openedWith });
	};
}
vi.mock("../chat/ChatSessionScreen", async () => ({ ChatSessionScreen: sessionSurface("ChatSessionScreen", await import("react")) }));
vi.mock("./CloudTerminalSessionScreen", async () => ({ CloudTerminalSessionScreen: sessionSurface("CloudTerminalSessionScreen", await import("react")) }));
vi.mock("./TerminalSessionScreen", async () => ({ default: sessionSurface("TerminalSessionScreen", await import("react")) }));
vi.mock("../RouteErrorBoundary", () => ({ RouteErrorBoundary: "RouteErrorBoundary" }));
vi.mock("../ui", () => ({ Button: "Button", EmptyState: "EmptyState" }));
vi.mock("../ThemeProvider", async () => {
	const { darkTheme } = await import("../theme");
	return { useTheme: () => darkTheme, useThemedStyles: (make: (theme: typeof darkTheme) => unknown) => make(darkTheme) };
});

import MobileSessionRoute from "../../app/session/[id]";

const cloudRef: SourceRef = { kind: "cloud", id: "org-1" };
const localRef: SourceRef = { kind: "local", id: "desktop-1" };
const session = (mode: "chat" | "tui", displayName: string): DashboardSession => ({
	id: "session-1", projectId: "project-1", kind: "worker", status: "working", mode,
	branch: null, issueId: null, issueTitle: null, userPrompt: null, displayName, summary: null,
	createdAt: "2026-10-02T00:00:00Z", lastActivityAt: "2026-10-02T00:00:00Z", runtimeConnected: true,
});

function setSessions(cloudMode: "chat" | "tui" = "chat", localMode: "chat" | "tui" = "tui") {
	const cloud = session(cloudMode, "Cloud worker");
	const local = session(localMode, "Desktop worker");
	fixture.board = {
		projects: [], orchestrators: [], sessions: [{ source: localRef, value: local }, { source: cloudRef, value: cloud }],
		sources: {
			'["cloud","org-1"]': { resolved: true, available: true, loading: false, stale: false, error: null },
			'["local","desktop-1"]': { resolved: true, available: true, loading: false, stale: false, error: null },
		},
	};
	return { cloud, local };
}

function handoff(phase: "draining" | "completed"): CloudInterfaceStatus {
	return {
		supported: true, targetMode: "tui", currentMode: phase === "completed" ? "tui" : "chat", runtimeConnected: true,
		transition: {
			id: "handoff-1", sessionId: "session-1", sourceMode: "chat", targetMode: "tui", policy: "drain", phase,
			createdAt: "2026-10-15T00:00:00Z", updatedAt: "2026-10-15T00:00:01Z",
		},
	};
}

let renderer: ReactTestRenderer | undefined;
const screen = (name: string) => renderer!.root.findByType(name as never);
async function renderRoute() { await act(async () => { renderer = create(<MobileSessionRoute />); }); }
async function updateRoute() { await act(async () => { renderer!.update(<MobileSessionRoute />); }); }

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	vi.clearAllMocks();
	fixture.params = { id: "session-1", source: "cloud", sourceId: "org-1" };
	fixture.status = undefined;
	fixture.auth = { signedIn: true, busy: false, orgId: "org-1", orgLoading: false, orgError: null };
	setSessions();
	fixture.sourceFor.mockImplementation((source: SourceRef) => source.kind === "cloud" ? fixture.cloudSource : fixture.localSource);
	fixture.configForHost.mockReturnValue({ hostId: "desktop-1" });
	fixture.interfaceHook.mockImplementation(() => ({
		status: fixture.status, transition: fixture.status?.transition, loading: false, starting: false, cancelling: false,
		startingPolicy: undefined, acknowledgingNotice: false, acknowledgeNoticeError: undefined, error: undefined, fetchFailed: false,
		refresh: fixture.interfaceRefresh, start: fixture.interfaceStart, cancel: fixture.interfaceCancel,
		acknowledgeNotice: fixture.interfaceAcknowledge,
	}));
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
});

afterEach(async () => {
	if (renderer) await act(async () => { renderer!.unmount(); });
	renderer = undefined;
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

describe("mobile session route composition", () => {
	it.each(["chat", "tui"] as const)("opens an existing reviewer without switching the %s worker interface", async (mode) => {
		const { cloud } = setSessions(mode);
		fixture.params = { ...fixture.params, view: "reviewer", terminalId: "00000000-0000-0000-0000-000000000091", scopeKey: "cloud-account:org-1", startup: "spawn" };
		fixture.status = handoff("completed");
		await renderRoute();
		expect(screen("CloudTerminalSessionScreen").props).toMatchObject({ session: cloud, source: cloudRef, reviewerTerminalId: fixture.params.terminalId });
		expect(screen("CloudTerminalSessionScreen").props.interfaceSwitch).toBeUndefined();
		expect(screen("CloudTerminalSessionScreen").props.showSpawnStartup).toBeUndefined();
		expect(fixture.interfaceHook.mock.calls.every(([source]) => source === undefined)).toBe(true);
		expect(fixture.interfaceRefresh).not.toHaveBeenCalled(); expect(fixture.interfaceStart).not.toHaveBeenCalled();
		expect(fixture.replace).not.toHaveBeenCalled(); expect(fixture.lookUpSession).not.toHaveBeenCalled();
	});

	it.each([
		{ terminalId: undefined }, { terminalId: "" }, { terminalId: "worker" }, { scopeKey: undefined }, { scopeKey: "old-account:org-1" },
		{ source: "local", sourceId: "desktop-1" }, { source: undefined, sourceId: undefined }, { hostId: "desktop-1" },
		{ view: "terminal" },
	])("rejects an unsafe reviewer target without opening a worker or Local surface: %j", async (invalid) => {
		fixture.params = { ...fixture.params, view: "reviewer", terminalId: "00000000-0000-0000-0000-000000000091", scopeKey: "cloud-account:org-1", ...invalid };
		await renderRoute();
		expect(renderer!.root.findAllByType("CloudTerminalSessionScreen" as never)).toHaveLength(0);
		expect(renderer!.root.findAllByType("TerminalSessionScreen" as never)).toHaveLength(0);
		expect(renderer!.root.findAllByType("ChatSessionScreen" as never)).toHaveLength(0);
		expect(screen("EmptyState").props.title).toMatch(/unavailable/i);
		expect(fixture.configForHost).not.toHaveBeenCalled(); expect(fixture.lookUpSession).not.toHaveBeenCalled();
		expect(fixture.interfaceRefresh).not.toHaveBeenCalled(); expect(fixture.refreshSource).not.toHaveBeenCalled();
	});

	it("closes a reviewer instead of adopting another account's colliding session", async () => {
		fixture.params = { ...fixture.params, view: "reviewer", terminalId: "00000000-0000-0000-0000-000000000091", scopeKey: "cloud-account:org-1" };
		await renderRoute();
		expect(screen("CloudTerminalSessionScreen").props.reviewerTerminalId).toBe(fixture.params.terminalId);
		fixture.sourceFor.mockReturnValue({ kind: "cloud", scopeKey: "other-account:org-1" });
		await updateRoute();
		expect(renderer!.root.findAllByType("CloudTerminalSessionScreen" as never)).toHaveLength(0);
		expect(screen("EmptyState").props.title).toMatch(/unavailable/i);
	});

	it("replaces reviewer buffers when opening a different review terminal on the same session", async () => {
		fixture.params = { ...fixture.params, view: "reviewer", terminalId: "00000000-0000-0000-0000-000000000091", scopeKey: "cloud-account:org-1" };
		await renderRoute();
		const openedWith = screen("CloudTerminalSessionScreen").props.openedWith;
		fixture.params = { ...fixture.params, terminalId: "00000000-0000-0000-0000-000000000092" };
		await updateRoute();
		expect(screen("CloudTerminalSessionScreen").props.openedWith).not.toBe(openedWith);
		expect(screen("CloudTerminalSessionScreen").props.reviewerTerminalId).toBe(fixture.params.terminalId);
	});
	it.each([
		{ mode: "chat", name: "ChatSessionScreen" },
		{ mode: "tui", name: "TerminalSessionScreen" },
	] as const)("opens a fresh Local $mode surface when another desktop has the same session ID", async ({ mode, name }) => {
		const { local } = setSessions("chat", mode);
		const otherLocal: SourceRef = { kind: "local", id: "desktop-2" };
		fixture.board.sessions.push({ source: otherLocal, value: { ...local, displayName: "Other desktop worker" } });
		fixture.board.sources['["local","desktop-2"]'] = fixture.board.sources['["local","desktop-1"]'];
		fixture.sourceFor.mockImplementation((source: SourceRef) => ({ kind: source.kind, scopeKey: source.id }));
		fixture.configForHost.mockImplementation((hostId: string) => ({ hostId }));
		fixture.params = { id: "session-1", hostId: "desktop-1" };
		await renderRoute();
		expect(screen(name).props.openedWith.source).toEqual(localRef);
		fixture.params = { id: "session-1", hostId: "desktop-2" };
		await updateRoute();
		expect(screen(name).props.openedWith).toEqual({ source: otherLocal, scopeKey: "desktop-2", sessionId: "session-1" });
	});

	it("opens a fresh Chat surface when Local and Cloud have the same session ID", async () => {
		setSessions("chat", "chat");
		fixture.params = { id: "session-1", hostId: "desktop-1" };
		await renderRoute();
		expect(screen("ChatSessionScreen").props.openedWith.source).toEqual(localRef);
		fixture.params = { id: "session-1", source: "cloud", sourceId: "org-1" };
		await updateRoute();
		expect(screen("ChatSessionScreen").props.openedWith).toEqual({ source: cloudRef, scopeKey: "cloud-account:org-1", sessionId: "session-1" });
	});

	it.each([
		{ mode: "chat", view: undefined, name: "ChatSessionScreen" },
		{ mode: "tui", view: undefined, name: "CloudTerminalSessionScreen" },
		{ mode: "chat", view: "shell", name: "CloudTerminalSessionScreen" },
	] as const)("replaces a Cloud $mode/$view surface when the account changes within the same organization", async ({ mode, view, name }) => {
		setSessions(mode);
		fixture.params = { ...fixture.params, view };
		await renderRoute();
		expect(screen(name).props.openedWith.scopeKey).toBe("cloud-account:org-1");
		fixture.sourceFor.mockReturnValue({ kind: "cloud", scopeKey: "other-account:org-1" });
		await updateRoute();
		expect(screen(name).props.openedWith).toEqual({ source: cloudRef, scopeKey: "other-account:org-1", sessionId: "session-1" });
	});

	it.each([
		{ source: "local", mode: "chat", name: "ChatSessionScreen" },
		{ source: "local", mode: "tui", name: "TerminalSessionScreen" },
		{ source: "cloud", mode: "chat", name: "ChatSessionScreen" },
		{ source: "cloud", mode: "tui", name: "CloudTerminalSessionScreen" },
	] as const)("retains the $source $mode surface through unchanged board and source object refreshes", async ({ source, mode, name }) => {
		setSessions(mode, mode);
		fixture.params = { id: "session-1", source, sourceId: source === "local" ? "desktop-1" : "org-1" };
		await renderRoute();
		const openedWith = screen(name).props.openedWith;
		fixture.board = { ...fixture.board, sessions: fixture.board.sessions.map((entry) => ({ ...entry, value: { ...entry.value } })) };
		fixture.sourceFor.mockImplementation((ref: SourceRef) => ({ ...(ref.kind === "cloud" ? fixture.cloudSource : fixture.localSource) }));
		await updateRoute();
		expect(screen(name).props.openedWith).toBe(openedWith);
	});

	it("opens a fresh Chat surface when the session ID changes on the same source", async () => {
		const { cloud } = setSessions();
		fixture.board.sessions.push({ source: cloudRef, value: { ...cloud, id: "session-2" } });
		await renderRoute();
		expect(screen("ChatSessionScreen").props.openedWith.sessionId).toBe("session-1");
		fixture.params = { ...fixture.params, id: "session-2" };
		await updateRoute();
		expect(screen("ChatSessionScreen").props.openedWith.sessionId).toBe("session-2");
	});

	it.each(["initial", "signing-in", "organization"])("keeps an absent Cloud source in its %s loading state, never Local pairing", async (stage) => {
		fixture.sourceFor.mockReturnValue(undefined); fixture.board = { ...fixture.board, sources: {} };
		fixture.auth = { ...fixture.auth, signedIn: stage === "initial" ? null : true, busy: stage === "signing-in", orgId: null, orgLoading: stage === "organization" };
		await renderRoute();
		expect(screen("ActivityIndicator")).toBeDefined();
		expect(renderer!.root.findAllByType("EmptyState" as never)).toHaveLength(0);
		expect(fixture.configForHost).not.toHaveBeenCalled(); expect(fixture.refreshSource).not.toHaveBeenCalled();
	});
	it("offers Cloud sign-in, not pairing, when an explicit Cloud route is signed out", async () => {
		fixture.sourceFor.mockReturnValue(undefined); fixture.board = { ...fixture.board, sources: {} };
		fixture.auth = { ...fixture.auth, signedIn: false, orgId: null };
		await renderRoute();
		const empty = screen("EmptyState");
		expect(empty.props.title).toBe("Sign in to Orchestrator Cloud");
		await act(async () => empty.props.action.props.onPress());
		expect(fixture.signIn).toHaveBeenCalledOnce(); expect(fixture.push).not.toHaveBeenCalled();
	});
	it.each([null, "org-1"])("retries workspace resolution, not a missing source refresh, for org %s", async (orgId) => {
		fixture.sourceFor.mockReturnValue(undefined); fixture.board = { ...fixture.board, sources: {} };
		fixture.auth = { ...fixture.auth, orgId, orgError: "Cloud is offline" };
		await renderRoute();
		expect(screen("EmptyState").props.message).toBe("Cloud is offline");
		await act(async () => screen("EmptyState").props.action.props.onPress());
		expect(fixture.retryOrg).toHaveBeenCalledOnce(); expect(fixture.refreshSource).not.toHaveBeenCalled();
		expect(fixture.configForHost).not.toHaveBeenCalled();
	});
	it("does not act on an explicit Cloud link from another organization", async () => {
		fixture.sourceFor.mockReturnValue(undefined); fixture.board = { ...fixture.board, sources: {} };
		fixture.auth.orgId = "another-org";
		await renderRoute();
		expect(screen("EmptyState").props.title).toBe("Cloud workspace changed");
		await act(async () => screen("EmptyState").props.action.props.onPress());
		expect(fixture.navigate).toHaveBeenCalledWith("/");
		expect(fixture.retryOrg).not.toHaveBeenCalled(); expect(fixture.signIn).not.toHaveBeenCalled(); expect(fixture.refreshSource).not.toHaveBeenCalled();
	});
	it("rediscovers Chat support after a same-mode restore without refetching on unchanged board rows", async () => {
		const { cloud } = setSessions("chat");
		cloud.isTerminated = true;
		fixture.status = { supported: false, targetMode: "tui", currentMode: "chat", reasonCode: "SESSION_TERMINATED" };
		await renderRoute();
		expect(fixture.interfaceRefresh).toHaveBeenCalledOnce();
		fixture.board = { ...fixture.board, sessions: fixture.board.sessions.map(entry => ({ ...entry, value: { ...entry.value, isTerminated: false } })) };
		await updateRoute();
		expect(fixture.interfaceRefresh).toHaveBeenCalledTimes(2);
		fixture.board = { ...fixture.board, sessions: fixture.board.sessions.map(entry => ({ ...entry, value: { ...entry.value } })) };
		await updateRoute();
		expect(fixture.interfaceRefresh).toHaveBeenCalledTimes(2);
	});
	it.each(["chat", "tui"] as const)("opens a Cloud %s worker's workspace shell using its organization source", async (mode) => {
		const { cloud } = setSessions(mode);
		fixture.params = { ...fixture.params, view: "shell", startup: "spawn" };
		await renderRoute();
		expect(screen("CloudTerminalSessionScreen").props).toMatchObject({ session: cloud, source: cloudRef, kind: "workspace" });
		expect(screen("CloudTerminalSessionScreen").props.interfaceSwitch).toBeUndefined();
		expect(screen("CloudTerminalSessionScreen").props.showSpawnStartup).toBeUndefined();
		expect(fixture.sourceFor).toHaveBeenCalledWith(cloudRef);
		expect(fixture.configForHost).not.toHaveBeenCalled();
		expect(fixture.lookUpSession).not.toHaveBeenCalled();
		expect(fixture.interfaceHook.mock.calls.every(([source]) => source === undefined)).toBe(true);
		expect(fixture.interfaceRefresh).not.toHaveBeenCalled();
		expect(fixture.refreshSource).not.toHaveBeenCalled();
	});

	it("keeps a workspace shell and its URL while an agent handoff becomes completed", async () => {
		fixture.params = { ...fixture.params, view: "shell" };
		fixture.status = handoff("draining");
		await renderRoute();
		expect(screen("CloudTerminalSessionScreen").props.kind).toBe("workspace");
		fixture.status = handoff("completed");
		setSessions("tui");
		await updateRoute();
		expect(screen("CloudTerminalSessionScreen").props).toMatchObject({ kind: "workspace", source: cloudRef });
		expect(fixture.replace).not.toHaveBeenCalled();
		expect(fixture.interfaceHook.mock.calls.every(([source]) => source === undefined)).toBe(true);
		expect(fixture.interfaceRefresh).not.toHaveBeenCalled();
	});

	it.each([
		{ mode: "chat", name: "ChatSessionScreen" },
		{ mode: "tui", name: "CloudTerminalSessionScreen" },
	] as const)("keeps ordinary Cloud $mode sessions connected to agent interface controls", async ({ mode, name }) => {
		const { cloud } = setSessions(mode);
		fixture.params = { ...fixture.params, startup: "spawn" };
		await renderRoute();
		const props = screen(name).props;
		expect(props).toMatchObject({ session: cloud, source: cloudRef });
		expect(props.kind).toBeUndefined();
		expect(mode === "chat" ? props.cloudInterface.refresh : props.interfaceSwitch.refresh).toBe(fixture.interfaceRefresh);
		if (mode === "tui") expect(props.showSpawnStartup).toBe(true);
		expect(fixture.interfaceHook).toHaveBeenCalledWith(fixture.cloudSource, "session-1", expect.any(Function));
		expect(fixture.interfaceRefresh).toHaveBeenCalledOnce();
		expect(fixture.replace).not.toHaveBeenCalled();
	});

	it("opens an explicit Cloud agent terminal without treating it as a workspace shell", async () => {
		fixture.params = { ...fixture.params, view: "terminal" };
		await renderRoute();
		const props = screen("CloudTerminalSessionScreen").props;
		expect(props.source).toEqual(cloudRef);
		expect(props.kind).toBeUndefined();
		expect(props.interfaceSwitch.refresh).toBe(fixture.interfaceRefresh);
		expect(fixture.interfaceRefresh).toHaveBeenCalledOnce();
	});

	it("still follows an ordinary agent handoff and clears its settled view override", async () => {
		fixture.params = { ...fixture.params, view: "terminal" };
		fixture.status = handoff("draining");
		await renderRoute();
		expect(screen("ChatSessionScreen").props.source).toEqual(cloudRef);
		expect(fixture.replace).not.toHaveBeenCalled();
		fixture.status = handoff("completed");
		await updateRoute();
		expect(screen("CloudTerminalSessionScreen").props.kind).toBeUndefined();
		expect(fixture.replace).toHaveBeenCalledExactlyOnceWith({ pathname: "/session/[id]", params: { id: "session-1", source: "cloud", sourceId: "org-1" } });
	});

	it.each([
		{ mode: "chat", name: "ChatSessionScreen" },
		{ mode: "tui", name: "TerminalSessionScreen" },
	] as const)("keeps Local $mode routing on the desktop even with a shell view parameter", async ({ mode, name }) => {
		const { local } = setSessions("chat", mode);
		fixture.params = { id: "session-1", hostId: "desktop-1", view: "shell" };
		fixture.status = handoff("completed");
		await renderRoute();
		expect(screen(name).props).toMatchObject({ session: local, source: localRef });
		expect(screen(name).props.cloudInterface).toBeUndefined();
		expect(fixture.configForHost).toHaveBeenCalledWith("desktop-1");
		expect(fixture.interfaceHook.mock.calls.every(([source]) => source === undefined)).toBe(true);
		expect(fixture.interfaceRefresh).not.toHaveBeenCalled();
		expect(fixture.lookUpSession).not.toHaveBeenCalled();
		expect(fixture.replace).not.toHaveBeenCalled();
	});
});
