import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactElement } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { WorkspaceSession } from "../types/workspace";
import type { SessionInterfaceTransitionStatus } from "./useSessionInterfaceTransition";
import { useSessionInterfaceSwitch } from "./useSessionInterfaceSwitch";

type SwitchDialogElement = ReactElement<{ open: boolean; requireExplicitTerminalStop: boolean }>;

const mocks = vi.hoisted(() => ({
	getSession: vi.fn(),
	start: vi.fn(),
	resetStartError: vi.fn(),
	context: vi.fn(),
	status: undefined as SessionInterfaceTransitionStatus | undefined,
	settling: false,
}));

vi.mock("./useCloudCp", () => ({ useCloudCp: () => ({ client: { getSession: mocks.getSession } }) }));
vi.mock("./useCloudGate", () => ({ useCloudGate: () => ({ cloudEnabled: true }) }));
vi.mock("./useSettings", () => ({ useSettings: () => ({ settings: { chatHarnesses: ["claude-code", "codex", "gemini", "cursor"] } }) }));
vi.mock("./useSessionInterfaceTransition", async (importOriginal) => ({
	...await importOriginal<typeof import("./useSessionInterfaceTransition")>(),
	useSessionInterfaceTransition: (sessionId: string, context: unknown) => {
		mocks.context(sessionId, context);
		return {
			status: mocks.status,
			transition: mocks.status?.transition,
			isLoading: false,
			statusError: undefined,
			start: mocks.start,
			starting: false,
			startingPolicy: undefined,
			settling: mocks.settling,
			startError: undefined,
			resetStartError: mocks.resetStartError,
			cancel: vi.fn(),
			cancelling: false,
			cancelError: undefined,
			acknowledgeNotice: vi.fn(),
			acknowledgingNotice: false,
			acknowledgeNoticeError: undefined,
			refreshStatus: vi.fn(),
		};
	},
}));

const cloudSession: WorkspaceSession = {
	id: "session-1",
	workspaceId: "project-1",
	workspaceName: "Project",
	title: "Agent",
	provider: "claude-code",
	kind: "worker",
	mode: "tui",
	status: "idle",
	updatedAt: "2026-10-01T00:00:00Z",
	prs: [],
	cloud: { orgId: "org-1" },
};

function requestSwitch(menuItem: ReactElement<{ onClick: () => void }> | null) {
	if (!menuItem) throw new Error("Switch action missing");
	act(() => menuItem.props.onClick());
}

describe("useSessionInterfaceSwitch Cloud handoff", () => {
	beforeEach(() => {
		localStorage.clear();
		mocks.getSession.mockReset();
		mocks.start.mockReset();
		mocks.start.mockResolvedValue({});
		mocks.resetStartError.mockReset();
		mocks.context.mockReset();
		mocks.status = { supported: true, targetMode: "chat" };
		mocks.settling = false;
	});

	it.each(["codex", "claude-code", "cursor"] as const)("preserves %s model and effort when leaving Cloud Chat", async (provider) => {
		mocks.status = { supported: true, targetMode: "tui" };
		localStorage.setItem(`cloud-chat-settings:org-1:session-1:${provider}`, JSON.stringify({
			model: "selected-model", reasoningEffort: "high",
		}));
		const { result } = renderHook(() => useSessionInterfaceSwitch("session-1", {
			...cloudSession, provider, mode: "chat",
		}, { orgId: "org-1" }));
		act(() => result.current.onConversationWorkChange({
			controllerBusy: false, hasRunningTurn: false, queuedTurnCount: 0,
		}));
		requestSwitch(result.current.menuItem as ReactElement<{ onClick: () => void }> | null);
		await waitFor(() => expect(mocks.start).toHaveBeenCalledWith({
			targetMode: "tui", policy: "drain", historyPolicy: "strict",
			model: "selected-model", reasoningEffort: "high",
		}));
	});

	it("does not treat a handoff that finished before the view opened as a switch in progress", () => {
		mocks.settling = true;
		mocks.status = {
			supported: true,
			targetMode: "tui",
			transition: {
				id: "finished-earlier",
				sessionId: "session-1",
				sourceMode: "tui",
				targetMode: "chat",
				policy: "drain",
				historyPolicy: "strict",
				phase: "completed",
				createdAt: "2026-10-01T00:00:00Z",
				updatedAt: "2026-10-01T00:00:01Z",
			},
		};
		const { cloud: _cloud, ...localSession } = cloudSession;
		const { result } = renderHook(() => useSessionInterfaceSwitch("session-1", { ...localSession, mode: "chat" }));
		expect(result.current.optimisticTarget).toBeUndefined();
		expect(result.current.controllerTransitioning).toBe(false);
	});

	it.each(["claude-code", "codex", "gemini", "cursor"] as const)("shows the terminal again when the daemon refuses a %s switch to Chat", async (provider) => {
		mocks.status = { supported: true, targetMode: "chat" };
		mocks.start.mockRejectedValueOnce(new Error("handoff refused"));
		const { cloud: _cloud, ...localSession } = cloudSession;
		const { result } = renderHook(() => useSessionInterfaceSwitch("session-1", { ...localSession, provider, mode: "tui", status: "idle" }));
		requestSwitch(result.current.menuItem as ReactElement<{ onClick: () => void }> | null);
		await waitFor(() => expect(mocks.start).toHaveBeenCalled());
		await waitFor(() => expect(result.current.optimisticTarget).toBeUndefined());
		expect(result.current.renderedMode).toBe("tui");
	});

	it("falls back to the source interface when a switch fails after it was shown", () => {
		mocks.status = {
			supported: true,
			targetMode: "chat",
			transition: {
				id: "failed-handoff",
				sessionId: "session-1",
				sourceMode: "tui",
				targetMode: "chat",
				policy: "drain",
				historyPolicy: "strict",
				phase: "failed",
				errorCode: "TARGET_RESUME_FAILED",
				createdAt: "2026-10-01T00:00:00Z",
				updatedAt: "2026-10-01T00:00:01Z",
			},
		};
		const { cloud: _cloud, ...localSession } = cloudSession;
		const { result } = renderHook(() => useSessionInterfaceSwitch("session-1", { ...localSession, mode: "tui" }));
		expect(result.current.optimisticTarget).toBeUndefined();
		expect(result.current.renderedMode).toBe("tui");
	});

	it("keeps source Chat visible while a Cloud drain waits and scopes transition to its org", () => {
		mocks.status = {
			supported: true,
			targetMode: "tui",
			transition: {
				id: "transition-1",
				sessionId: "session-1",
				sourceMode: "chat",
				targetMode: "tui",
				policy: "drain",
				historyPolicy: "strict",
				phase: "draining",
				createdAt: "2026-10-01T00:00:00Z",
				updatedAt: "2026-10-01T00:00:01Z",
			},
		};
		const { result } = renderHook(() => useSessionInterfaceSwitch("session-1", { ...cloudSession, mode: "chat", status: "working" }, { orgId: "org-1" }));
		expect(mocks.context).toHaveBeenCalledWith("session-1", { orgId: "org-1" });
		expect(result.current.cloudLoader).toBe(false);
		expect(result.current.controllerTransitioning).toBe(false);
		expect(result.current.newWorkDisabled).toBe(true);
		expect(result.current.inlineStatus).not.toBeNull();
	});

	it("checks fresh Cloud activity before automatically interrupting an idle terminal", async () => {
		mocks.getSession.mockResolvedValue({ session: { activityState: "idle", status: "idle" } });
		const { result } = renderHook(() => useSessionInterfaceSwitch("session-1", cloudSession, { orgId: "org-1" }));
		requestSwitch(result.current.menuItem as ReactElement<{ onClick: () => void }> | null);
		await waitFor(() => expect(mocks.start).toHaveBeenCalledWith({ targetMode: "chat", policy: "interrupt", historyPolicy: "strict" }));
		expect(mocks.getSession).toHaveBeenCalledWith("org-1", "session-1");
	});

	it("asks before stopping when the fresh Cloud row reports work", async () => {
		mocks.getSession.mockResolvedValue({ session: { activityState: "active", status: "working" } });
		const { result } = renderHook(() => useSessionInterfaceSwitch("session-1", cloudSession, { orgId: "org-1" }));
		requestSwitch(result.current.menuItem as ReactElement<{ onClick: () => void }> | null);
		await waitFor(() => expect((result.current.dialogs as ReactElement<{ children: SwitchDialogElement[] }>).props.children[0].props.open).toBe(true));
		expect(mocks.start).not.toHaveBeenCalled();
	});

	it("requires an explicit stop when Cloud activity cannot be checked", async () => {
		mocks.getSession.mockRejectedValue(new Error("offline"));
		const { result } = renderHook(() => useSessionInterfaceSwitch("session-1", cloudSession, { orgId: "org-1" }));
		requestSwitch(result.current.menuItem as ReactElement<{ onClick: () => void }> | null);
		await waitFor(() => expect((result.current.dialogs as ReactElement<{ children: SwitchDialogElement[] }>).props.children[0].props.requireExplicitTerminalStop).toBe(true));
		expect(mocks.start).not.toHaveBeenCalled();
	});
});
