import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { useUiStore } from "../stores/ui-store";
import type { WorkspaceSession } from "../types/workspace";
import { useSideChatTab, useSideChatsAvailable } from "./side-chats";

const session: WorkspaceSession = {
	id: "chat", workspaceId: "project", workspaceName: "Project", title: "Chat",
	provider: "kimi", mode: "chat", status: "working", updatedAt: "", prs: [],
};

beforeEach(() => useUiStore.setState({ developerMode: true, sideChats: true }));
afterEach(() => useUiStore.setState({ developerMode: false, sideChats: false }));

it.each(["codex", "claude-code", "opencode", "opencode-v2", "droid", "kimi", "kimchi", "pi", "cursor", "omp", "qwen", "unreal-agent", "deepseek-harness", "command-code"] as const)(
	"allows admitted local Chat sessions for %s without a provider allowlist",
	(provider) => {
		const { result } = renderHook(() => useSideChatsAvailable({ ...session, provider }));
		expect(result.current).toBe(true);
	},
);

it("keeps side conversations accessible while their parent is idle", () => {
	const { result } = renderHook(() => useSideChatsAvailable({ ...session, status: "idle" }));
	expect(result.current).toBe(true);
});

const restrictions: Array<Partial<WorkspaceSession> & { host?: string }> = [
	{ host: "remote" }, { mode: "tui" }, { cloud: { orgId: "org" } }, { isTerminated: true },
];
it.each(restrictions)("keeps local live Chat restrictions: %j", (restriction) => {
	const { result } = renderHook(() => useSideChatsAvailable({ ...session, ...restriction }, restriction.host));
	expect(result.current).toBe(false);
});

it("requires both developer mode and the side-chat experiment", () => {
	useUiStore.setState({ sideChats: false });
	const { result } = renderHook(() => useSideChatsAvailable(session));
	expect(result.current).toBe(false);
});

describe("Side chat tab", () => {
	const key = "chat";
	const view = () => useUiStore.getState().inspectorSessions[key]?.view;
	beforeEach(() => useUiStore.getState().setInspectorView(key, "summary"));
	afterEach(() => useUiStore.setState({ inspectorSessions: {} }));

	it("stays hidden until the session has a side chat", () => {
		const { result } = renderHook(() => useSideChatTab(key, true));
		expect(result.current.showTab).toBe(false);
		act(() => result.current.onSideChatsChange({ present: false, creating: false }));
		expect(result.current.showTab).toBe(false);
		act(() => result.current.onSideChatsChange({ present: true, creating: false }));
		expect(result.current.showTab).toBe(true);
	});

	it("keeps Side chat selected when the first empty list lands while a side chat is being created", () => {
		const { result } = renderHook(() => useSideChatTab(key, true));
		// /btw selects the tab and starts creating before the list has loaded.
		act(() => useUiStore.getState().setInspectorView(key, "sideChat"));
		act(() => result.current.onSideChatsChange({ present: false, creating: true }));
		expect(view()).toBe("sideChat");
		expect(result.current.showTab).toBe(true);
		act(() => result.current.onSideChatsChange({ present: true, creating: false }));
		expect(view()).toBe("sideChat");
		expect(result.current.showTab).toBe(true);
	});

	it("returns to Summary when a creation fails and no side chat exists", () => {
		const { result } = renderHook(() => useSideChatTab(key, true));
		act(() => result.current.onSideChatsChange({ present: false, creating: false }));
		act(() => useUiStore.getState().setInspectorView(key, "sideChat"));
		act(() => result.current.onSideChatsChange({ present: false, creating: true }));
		expect(view()).toBe("sideChat");
		act(() => result.current.onSideChatsChange({ present: false, creating: false }));
		expect(view()).toBe("summary");
		expect(result.current.showTab).toBe(false);
	});

	it("returns to Summary when the last side chat closes", () => {
		const { result } = renderHook(() => useSideChatTab(key, true));
		act(() => result.current.onSideChatsChange({ present: true, creating: false }));
		act(() => useUiStore.getState().setInspectorView(key, "sideChat"));
		act(() => result.current.onSideChatsChange({ present: false, creating: false }));
		expect(view()).toBe("summary");
		expect(result.current.showTab).toBe(false);
	});

	it("never shows the tab where side chats are unavailable", () => {
		const { result } = renderHook(() => useSideChatTab(key, false));
		act(() => result.current.onSideChatsChange({ present: true, creating: false }));
		expect(result.current.showTab).toBe(false);
	});
});
