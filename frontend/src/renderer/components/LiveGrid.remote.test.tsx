import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import type { WorkspaceSession, WorkspaceSummary } from "../types/workspace";
import { LiveGrid } from "./LiveGrid";
import { OpenGridButton } from "./OpenGridButton";
import { TooltipProvider } from "./ui/tooltip";

const mocks = vi.hoisted(() => ({
	navigate: vi.fn(), restore: vi.fn(), conversation: vi.fn(),
	connected: true, healthy: true, workspaces: {} as Record<string, WorkspaceSummary>,
}));
vi.mock("@tanstack/react-router", async (original) => ({
	...await original<typeof import("@tanstack/react-router")>(), useNavigate: () => mocks.navigate,
}));
vi.mock("../hooks/useWorkspaceQuery", () => ({
	useWorkspaceQuery: () => ({ data: [{ id: "shared", sessions: [session("local", "tui")] }], isSuccess: true, isError: false }),
	useRemoteProjectQuery: (hostId: string) => ({ data: mocks.workspaces[hostId], isSuccess: mocks.healthy, isError: !mocks.healthy, refetch: vi.fn() }),
}));
vi.mock("../hooks/useHostConnection", () => ({
	useHostConnection: (hostId: string) => ({ baseUrl: mocks.connected ? `http://proxy/${hostId}` : undefined, label: hostId }),
}));
vi.mock("../lib/shell-context", () => ({ useShell: () => ({ daemonStatus: { state: "stopped" } }) }));
vi.mock("../hooks/useRestoreSession", () => ({ useRestoreSession: () => mocks.restore }));
vi.mock("../hooks/useConversation", () => ({ useConversation: (id?: string, hostId?: string) => { mocks.conversation(id, hostId); return { snapshot: { settings: { model: "test-model" } } }; } }));
vi.mock("./ShellTopbar", () => ({ ShellTopbar: () => null }));
vi.mock("./RestoreUnavailableDialog", () => ({ RestoreUnavailableDialog: () => null }));
vi.mock("./TerminalPane", () => ({ TerminalPane: ({ session, daemonReady }: { session: WorkspaceSession; daemonReady: boolean }) =>
	<div data-testid="terminal" data-host-id={session.hostId} data-ready={String(daemonReady)}><input aria-label={`terminal-${session.id}`} /></div> }));
vi.mock("./chat/SessionChatCache", () => ({ RetainedSessionChatSurface: ({ session, hostId, assetBaseUrl }: { session: WorkspaceSession; hostId?: string; assetBaseUrl?: string }) =>
	<div data-testid="chat" data-host-id={hostId} data-assets={assetBaseUrl}><textarea aria-label={`chat-${session.id}`} /></div> }));

function session(id: string, mode: "tui" | "chat", kind: "worker" | "orchestrator" = "worker", hostId = "box-a"): WorkspaceSession {
	return { id, hostId, mode, kind, workspaceId: "shared", workspaceName: "Project", title: id, provider: "opencode", status: "working", updatedAt: "now", createdAt: id, prs: [] };
}
function project(hostId = "box-a"): WorkspaceSummary {
	return { id: "shared", hostId, name: hostId, path: "/fixture", kind: "single_repo", sessions: [session("worker", "chat", "worker", hostId), session("orch", "tui", "orchestrator", hostId)] };
}
function tree(hostId = "box-a") {
	return <TooltipProvider><LiveGrid key={hostId} projectId="shared" hostId={hostId} /></TooltipProvider>;
}
beforeEach(() => {
	localStorage.clear(); mocks.navigate.mockReset(); mocks.conversation.mockClear();
	mocks.restore.mockReset().mockResolvedValue({ status: "success" });
	mocks.connected = true; mocks.healthy = true; mocks.workspaces = { "box-a": project(), "box-b": project("box-b") };
});

it("pins the remote orchestrator, renders the owning host's Chat/TUI and does not require the local daemon", () => {
	render(tree());
	const panels = screen.getAllByTestId("grid-panel");
	expect(panels[0]).toHaveAttribute("data-session-id", "orch");
	expect(within(panels[0]).getByTestId("terminal")).toHaveAttribute("data-host-id", "box-a");
	expect(within(panels[0]).getByTestId("terminal")).toHaveAttribute("data-ready", "true");
	expect(screen.getByTestId("chat")).toHaveAttribute("data-host-id", "box-a");
	expect(screen.getByTestId("chat")).toHaveAttribute("data-assets", "http://proxy/box-a");
	expect(mocks.conversation).toHaveBeenCalledWith("worker", "box-a");
	expect(document.querySelector('[data-session-id="local"]')).toBeNull();
	expect(mocks.restore).not.toHaveBeenCalled();
});

it("adds workers without remounting existing panels, hides completed workers and preserves their ordering", () => {
	const view = render(tree());
	const input = screen.getByRole("textbox", { name: "chat-worker" });
	fireEvent.change(input, { target: { value: "draft" } });
	mocks.workspaces["box-a"].sessions = [...mocks.workspaces["box-a"].sessions, session("next", "tui")];
	view.rerender(tree());
	expect(screen.getAllByTestId("grid-panel").map((node) => node.dataset.sessionId)).toEqual(["orch", "worker", "next"]);
	expect(screen.getByRole("textbox", { name: "chat-worker" })).toBe(input);
	mocks.workspaces["box-a"].sessions = mocks.workspaces["box-a"].sessions.map((item) => item.id === "next" ? { ...item, isTerminated: true } : item);
	view.rerender(tree());
	expect(screen.getAllByTestId("grid-panel")).toHaveLength(2);
	fireEvent.click(screen.getByRole("checkbox"));
	expect(screen.getAllByTestId("grid-panel")).toHaveLength(3);
	expect(input).toHaveValue("draft");
	expect(mocks.restore).not.toHaveBeenCalled();
});

it("keeps panels and drafts during an offline snapshot and restores the existing connection", () => {
	const view = render(tree());
	const input = screen.getByRole("textbox", { name: "chat-worker" });
	fireEvent.change(input, { target: { value: "keep" } });
	mocks.connected = false; mocks.healthy = false;
	view.rerender(tree());
	expect(screen.getByRole("alert")).toHaveTextContent("Host is offline");
	expect(screen.getAllByTestId("grid-panel")).toHaveLength(2);
	expect(screen.getByRole("textbox", { name: "chat-worker" })).toBe(input);
	mocks.connected = true; mocks.healthy = true;
	view.rerender(tree());
	expect(screen.queryByRole("alert")).toBeNull();
	expect(input).toHaveValue("keep");
	expect(mocks.restore).not.toHaveBeenCalled();
});

it("navigates Grid and Kanban on the selected host and scopes saved preferences between equal project IDs", async () => {
	const view = render(tree());
	fireEvent.click(screen.getByRole("checkbox"));
	fireEvent.click(screen.getByRole("button", { name: "Open Kanban" }));
	expect(mocks.navigate).toHaveBeenCalledWith({ to: "/host/$hostId/project/$projectId", params: { hostId: "box-a", projectId: "shared" } });
	view.rerender(tree("box-b"));
	expect(screen.getByRole("checkbox")).not.toBeChecked();
	view.rerender(tree());
	expect(screen.getByRole("checkbox")).toBeChecked();
	view.unmount();
	render(<TooltipProvider><OpenGridButton projectId="shared" hostId="box-b" /></TooltipProvider>);
	fireEvent.click(screen.getByRole("button", { name: "Open Grid" }));
	expect(mocks.navigate).toHaveBeenCalledWith({ to: "/host/$hostId/project/$projectId/grid", params: { hostId: "box-b", projectId: "shared" } });
	await waitFor(() => expect(localStorage.getItem("ao.live-grid.v1.remote.box-a.shared")).toContain('"showCompleted":true'));
});

it("uses the existing host-qualified restore only after an explicit action", async () => {
	mocks.workspaces["box-a"].sessions[1].isTerminated = true;
	render(tree());
	expect(mocks.restore).not.toHaveBeenCalled();
	fireEvent.click(screen.getByRole("button", { name: "Restore session" }));
	await waitFor(() => expect(mocks.restore).toHaveBeenCalledWith("orch", "box-a"));
});
