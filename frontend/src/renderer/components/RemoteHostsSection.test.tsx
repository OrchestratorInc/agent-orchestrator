import { fireEvent, render as rtlRender, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactElement } from "react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import { RemoteHostsSection } from "./RemoteHostsSection";
import { SidebarMenu, SidebarProvider } from "./ui/sidebar";
import { TooltipProvider } from "./ui/tooltip";
import { STANDALONE_WORKSPACE_ID } from "../types/workspace";

const remotePatch = vi.hoisted(() => vi.fn().mockResolvedValue({ data: {}, error: undefined, response: { status: 200 } }));
const remotePost = vi.hoisted(() => vi.fn().mockResolvedValue({ data: {}, error: undefined, response: { status: 200 } }));
const remoteClient = vi.hoisted(() => vi.fn((_hostId: string) => ({ PATCH: remotePatch, POST: remotePost, DELETE: vi.fn() })));
vi.mock("../lib/host-clients", async (importOriginal) => ({
	...await importOriginal<typeof import("../lib/host-clients")>(),
	clientForSessionHost: remoteClient,
	clientForHost: remoteClient,
}));

function render(ui: ReactElement) {
	return rtlRender(<QueryClientProvider client={new QueryClient()}>{ui}</QueryClientProvider>);
}

it("keeps offline hosts visible and opens a same-ID session on the selected host", () => {
	const open = vi.fn();
	const retry = vi.fn();
	const addProject = vi.fn();
	render(<TooltipProvider><SidebarProvider><SidebarMenu><RemoteHostsSection
		hosts={[
			{ hostId: "box-a", label: "Box A", url: "http://box-a:3001", status: "offline" },
			{ hostId: "box-b", label: "Box B", url: "http://box-b:3001", status: "connected" },
		]}
		workspaces={[{
			hostId: "box-a", id: "project-a", name: "Old project", path: "/stale", sessions: [{
				hostId: "box-a", id: "old-session", workspaceId: "project-a", workspaceName: "Old project",
				title: "Stale session", provider: "codex", status: "working", updatedAt: "2026-01-01T00:00:00Z", prs: [],
			}],
		}, {
			hostId: "box-b", id: "project-1", name: "Agent Repo", path: "/remote", sessions: [{
				hostId: "box-b", id: "session-1", workspaceId: "project-1", workspaceName: "Agent Repo",
				title: "Fix login", provider: "codex", status: "working", updatedAt: "2026-01-01T00:00:00Z", prs: [],
			}],
		}]}
		loadedProjectHostIds={["box-b"]}
		onOpenSession={open}
		onOpenProject={vi.fn()}
		onOpenHome={vi.fn()}
		onNewTask={vi.fn()}
		onOrchestrator={vi.fn()}
		onConfigure={vi.fn()}
		onAddProject={addProject}
		onRemoveProject={vi.fn()}
		onRetry={retry}
	/></SidebarMenu></SidebarProvider></TooltipProvider>);
	expect(screen.getByText("Box A")).toBeVisible();
	expect(screen.queryByText("Stale session")).not.toBeInTheDocument();
	expect(screen.queryByRole("button", { name: "Spawn Old project · Box A orchestrator" })).not.toBeInTheDocument();
	fireEvent.click(screen.getByRole("button", { name: "Retry Box A" }));
	expect(retry).toHaveBeenCalledOnce();
	expect(screen.queryByRole("button", { name: "Start on Box B" })).not.toBeInTheDocument();
	const add = screen.getByRole("button", { name: "Add project on Box B" });
	expect(add).toHaveTextContent("1 project");
	fireEvent.click(add);
	expect(addProject).toHaveBeenCalledWith("box-b");
	expect(screen.queryByRole("button", { name: "Add project on Box A" })).not.toBeInTheDocument();
	fireEvent.click(screen.getByRole("button", { name: "Open Fix login" }));
	expect(open).toHaveBeenCalledWith("box-b", "project-1", "session-1");
});

it("routes project actions to the selected host when project IDs collide", async () => {
	const user = userEvent.setup();
	const openProject = vi.fn();
	const newTask = vi.fn();
	const orchestrator = vi.fn();
	const configure = vi.fn();
	render(<TooltipProvider><SidebarProvider><SidebarMenu><RemoteHostsSection
		hosts={[
			{ hostId: "box-a", label: "Box A", url: "http://box-a:3001", status: "connected" },
			{ hostId: "box-b", label: "Box B", url: "http://box-b:3001", status: "connected" },
		]}
		workspaces={[
			{ hostId: "box-a", id: "shared", name: "Shared", path: "/a", sessions: [] },
			{ hostId: "box-b", id: "shared", name: "Shared", path: "/b", sessions: [] },
		]}
		onOpenSession={vi.fn()}
		onOpenProject={openProject}
		onOpenHome={vi.fn()}
		onNewTask={newTask}
		onOrchestrator={orchestrator}
		onConfigure={configure}
		onAddProject={vi.fn()}
		onRemoveProject={vi.fn()}
		onRetry={vi.fn()}
	/></SidebarMenu></SidebarProvider></TooltipProvider>);

	await user.click(screen.getByRole("button", { name: "Toggle Shared · Box B sessions" }));
	expect(openProject).not.toHaveBeenCalled();
	await user.click(screen.getByRole("button", { name: "Open Shared · Box B dashboard" }));
	expect(openProject).toHaveBeenCalledWith("box-b", "shared");
	await user.click(screen.getByRole("button", { name: "Spawn Shared · Box B orchestrator" }));
	expect(orchestrator).toHaveBeenCalledWith("box-b", "shared");
	await user.click(screen.getByRole("button", { name: "Project actions for Shared on Box B" }));
	await user.click(await screen.findByRole("menuitem", { name: "New task" }));
	expect(newTask).toHaveBeenCalledWith("box-b", "shared");
	await user.click(screen.getByRole("button", { name: "Project actions for Shared on Box B" }));
	await user.click(await screen.findByRole("menuitem", { name: "Project settings" }));
	expect(configure).toHaveBeenCalledWith("box-b", "shared");
});

it("uses the local project context menu on the right remote host", async () => {
	const user = userEvent.setup();
	const newTask = vi.fn();
	const configure = vi.fn();
	render(<TooltipProvider><SidebarProvider><SidebarMenu><RemoteHostsSection
		hosts={[
			{ hostId: "box-a", label: "Box A", url: "http://box-a:3001", status: "connected" },
			{ hostId: "box-b", label: "Box B", url: "http://box-b:3001", status: "connected" },
		]}
		workspaces={[
			{ hostId: "box-a", id: "shared", name: "Shared", path: "/a", sessions: [] },
			{ hostId: "box-b", id: "shared", name: "Shared", path: "/b", sessions: [] },
		]}
		onOpenSession={vi.fn()} onOpenProject={vi.fn()} onOpenHome={vi.fn()} onNewTask={newTask} onOrchestrator={vi.fn()}
		onConfigure={configure} onAddProject={vi.fn()} onRemoveProject={vi.fn()} onRetry={vi.fn()}
	/></SidebarMenu></SidebarProvider></TooltipProvider>);

	fireEvent.contextMenu(screen.getByRole("button", { name: "Open Shared · Box B dashboard" }));
	const menu = await screen.findByRole("menu");
	expect(within(menu).getAllByRole("menuitem").map((item) => item.textContent)).toEqual(["New task", "Project settings", "Remove project"]);
	await user.click(within(menu).getByRole("menuitem", { name: "Project settings" }));
	expect(configure).toHaveBeenCalledWith("box-b", "shared");
	expect(newTask).not.toHaveBeenCalled();
});

it("collapses an already selected remote project when its row is clicked again", async () => {
	const user = userEvent.setup();
	const openProject = vi.fn();
	render(<TooltipProvider><SidebarProvider><SidebarMenu><RemoteHostsSection
		hosts={[{ hostId: "box-a", label: "Box A", url: "http://box-a:3001", status: "connected" }]}
		workspaces={[{ hostId: "box-a", id: "project-a", name: "Project A", path: "/a", sessions: [{
			hostId: "box-a", id: "session-a", workspaceId: "project-a", workspaceName: "Project A",
			title: "Worker", provider: "opencode", status: "working", updatedAt: "2026-09-28T00:00:00Z", prs: [],
		}]}]}
		activeHostId="box-a" activeProjectId="project-a"
		onOpenSession={vi.fn()} onOpenProject={openProject} onOpenHome={vi.fn()} onNewTask={vi.fn()} onOrchestrator={vi.fn()}
		onConfigure={vi.fn()} onAddProject={vi.fn()} onRemoveProject={vi.fn()} onRetry={vi.fn()}
	/></SidebarMenu></SidebarProvider></TooltipProvider>);

	const project = screen.getByRole("button", { name: "Open Project A · Box A dashboard" });
	expect(screen.getByRole("button", { name: "Open Worker" })).toBeVisible();
	await user.click(project);
	expect(screen.queryByRole("button", { name: "Open Worker" })).not.toBeInTheDocument();
	expect(openProject).not.toHaveBeenCalled();
	await user.click(project);
	expect(screen.getByRole("button", { name: "Open Worker" })).toBeVisible();
	expect(openProject).toHaveBeenCalledWith("box-a", "project-a");
});

it("renames, pins, and archives only the selected host's same-ID session", async () => {
	const user = userEvent.setup();
	remoteClient.mockClear();
	remotePatch.mockClear();
	remotePost.mockClear();
	render(<TooltipProvider><SidebarProvider><SidebarMenu><RemoteHostsSection
		hosts={[
			{ hostId: "box-a", label: "Box A", url: "http://box-a:3001", status: "connected" },
			{ hostId: "box-b", label: "Box B", url: "http://box-b:3001", status: "connected" },
		]}
		workspaces={["box-a", "box-b"].map((hostId) => ({ hostId, id: "shared", name: "Shared", path: "/remote", sessions: [{
			hostId: hostId === "box-a" ? hostId : undefined, id: "same", workspaceId: "shared", workspaceName: "Shared", title: "Same title",
			provider: "opencode" as const, status: "working" as const, updatedAt: "2026-09-28T00:00:00Z", prs: [],
		}] }))}
		onOpenSession={vi.fn()} onOpenProject={vi.fn()} onOpenHome={vi.fn()} onNewTask={vi.fn()} onOrchestrator={vi.fn()}
		onConfigure={vi.fn()} onAddProject={vi.fn()} onRemoveProject={vi.fn()} onRetry={vi.fn()}
	/></SidebarMenu></SidebarProvider></TooltipProvider>);

	const hostB = document.querySelector('[data-remote-project-row][data-host-id="box-b"]');
	if (!hostB) throw new Error("Host B project missing");
	fireEvent.contextMenu(within(hostB as HTMLElement).getByRole("button", { name: "Open Same title" }));
	await user.click(await screen.findByRole("menuitem", { name: "Rename Same title" }));
	const editor = within(hostB as HTMLElement).getByRole("textbox", { name: "Rename Same title" });
	await user.clear(editor);
	await user.type(editor, "Box B title{Enter}");
	await waitFor(() => expect(remotePatch).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}", { params: { path: { sessionId: "same" } }, body: { displayName: "Box B title" } }));
	await user.click(within(hostB as HTMLElement).getByRole("button", { name: "Pin session" }));
	await waitFor(() => expect(remotePost).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/pin", { params: { path: { sessionId: "same" } } }));
	await user.click(within(hostB as HTMLElement).getByRole("button", { name: "Archive session" }));
	const archive = await screen.findByRole("dialog");
	await user.click(within(archive).getByRole("button", { name: "Confirm, archive session" }));
	await waitFor(() => expect(remotePost).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/kill", { params: { path: { sessionId: "same" } } }));
	expect(remoteClient.mock.calls.every(([hostId]) => hostId === "box-b")).toBe(true);
});

it("highlights a standalone session on its host without a project route", () => {
	render(<TooltipProvider><SidebarProvider><SidebarMenu><RemoteHostsSection
		hosts={[{ hostId: "box-b", label: "Box B", url: "http://box-b:3001", status: "connected" }]}
		workspaces={[{
			hostId: "box-b", id: STANDALONE_WORKSPACE_ID, name: "Standalone", path: "", sessions: [{
				hostId: "box-b", id: "session-1", workspaceId: STANDALONE_WORKSPACE_ID, workspaceName: "Standalone",
				title: "Investigate", provider: "opencode", status: "working", updatedAt: "2026-09-28T00:00:00Z", prs: [],
			}],
		}]}
		activeHostId="box-b"
		activeSessionId="session-1"
		onOpenSession={vi.fn()} onOpenProject={vi.fn()} onOpenHome={vi.fn()} onNewTask={vi.fn()} onOrchestrator={vi.fn()}
		onConfigure={vi.fn()} onAddProject={vi.fn()} onRemoveProject={vi.fn()} onRetry={vi.fn()}
	/></SidebarMenu></SidebarProvider></TooltipProvider>);
	expect(screen.getByRole("button", { name: "Open Investigate" })).toHaveAttribute("aria-current", "page");
});

it("returns home when the last remote standalone session is archived", async () => {
	const user = userEvent.setup();
	const openHome = vi.fn();
	const openProject = vi.fn();
	render(<TooltipProvider><SidebarProvider><SidebarMenu><RemoteHostsSection
		hosts={[{ hostId: "box-b", label: "Box B", url: "http://box-b:3001", status: "connected" }]}
		workspaces={[{ hostId: "box-b", id: STANDALONE_WORKSPACE_ID, name: "Standalone", path: "", sessions: [{
			id: "same", workspaceId: STANDALONE_WORKSPACE_ID, workspaceName: "Standalone", title: "Investigate",
			provider: "opencode", status: "working", updatedAt: "2026-09-28T00:00:00Z", prs: [],
		}] }]}
		activeHostId="box-b" activeSessionId="same"
		onOpenSession={vi.fn()} onOpenProject={openProject} onOpenHome={openHome} onNewTask={vi.fn()} onOrchestrator={vi.fn()}
		onConfigure={vi.fn()} onAddProject={vi.fn()} onRemoveProject={vi.fn()} onRetry={vi.fn()}
	/></SidebarMenu></SidebarProvider></TooltipProvider>);

	await user.click(screen.getByRole("button", { name: "Archive session" }));
	await user.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Confirm, archive session" }));
	await waitFor(() => expect(openHome).toHaveBeenCalledOnce());
	expect(openProject).not.toHaveBeenCalled();
});

it("shows a retry action when a connected host cannot load its sessions", () => {
	const retry = vi.fn();
	render(<TooltipProvider><SidebarProvider><SidebarMenu><RemoteHostsSection
		hosts={[{ hostId: "box-a", label: "Box A", url: "http://box-a:3001", status: "connected" }]}
		workspaces={[]}
		failedHostIds={["box-a"]}
		loadedProjectHostIds={["box-a"]}
		onOpenSession={vi.fn()}
		onOpenProject={vi.fn()}
		onOpenHome={vi.fn()}
		onNewTask={vi.fn()}
		onOrchestrator={vi.fn()}
		onConfigure={vi.fn()}
		onAddProject={vi.fn()}
		onRemoveProject={vi.fn()}
		onRetry={retry}
	/></SidebarMenu></SidebarProvider></TooltipProvider>);
	expect(screen.getByRole("button", { name: "Add project on Box A" })).toHaveTextContent("0 projects");
	fireEvent.click(screen.getByRole("button", { name: "Could not load sessions. Retry" }));
	expect(retry).toHaveBeenCalledOnce();
});

it("does not show a false zero before project loading succeeds", () => {
	render(<TooltipProvider><SidebarProvider><SidebarMenu><RemoteHostsSection
		hosts={[{ hostId: "box-a", label: "Box A", url: "http://box-a:3001", status: "connected" }]}
		workspaces={[]}
		onOpenSession={vi.fn()}
		onOpenProject={vi.fn()}
		onOpenHome={vi.fn()}
		onNewTask={vi.fn()}
		onOrchestrator={vi.fn()}
		onConfigure={vi.fn()}
		onAddProject={vi.fn()}
		onRemoveProject={vi.fn()}
		onRetry={vi.fn()}
	/></SidebarMenu></SidebarProvider></TooltipProvider>);
	expect(screen.getByRole("button", { name: "Add project on Box A" })).not.toHaveTextContent("0");
});

it("shows a password hint when a saved host rejects its credential", () => {
	render(<TooltipProvider><SidebarProvider><SidebarMenu><RemoteHostsSection
		hosts={[{ hostId: "box-a", label: "Box A", url: "http://box-a:3001", status: "offline", failureReason: "unauthorized" }]}
		workspaces={[]}
		onOpenSession={vi.fn()}
		onOpenProject={vi.fn()}
		onOpenHome={vi.fn()}
		onNewTask={vi.fn()}
		onOrchestrator={vi.fn()}
		onConfigure={vi.fn()}
		onAddProject={vi.fn()}
		onRemoveProject={vi.fn()}
		onRetry={vi.fn()}
	/></SidebarMenu></SidebarProvider></TooltipProvider>);
	expect(screen.getByRole("button", { name: "Retry Box A" })).toHaveTextContent("Password rejected");
});

it("shows an update hint when a host API is incompatible", () => {
	render(<TooltipProvider><SidebarProvider><SidebarMenu><RemoteHostsSection
		hosts={[{ hostId: "box-a", label: "Box A", url: "http://box-a:3001", status: "offline", failureReason: "incompatible" }]}
		workspaces={[]}
		onOpenSession={vi.fn()}
		onOpenProject={vi.fn()}
		onOpenHome={vi.fn()}
		onNewTask={vi.fn()}
		onOrchestrator={vi.fn()}
		onConfigure={vi.fn()}
		onAddProject={vi.fn()}
		onRemoveProject={vi.fn()}
		onRetry={vi.fn()}
	/></SidebarMenu></SidebarProvider></TooltipProvider>);
	expect(screen.getByRole("button", { name: "Retry Box A" })).toHaveTextContent("Update AO");
});

it("confirms removal on the selected host and disables its action while pending", async () => {
	const user = userEvent.setup();
	let finishRemove!: () => void;
	const removeProject = vi.fn(() => new Promise<void>((resolve) => { finishRemove = resolve; }));
	render(<TooltipProvider><SidebarProvider><SidebarMenu><RemoteHostsSection
		hosts={[
			{ hostId: "box-a", label: "Box A", url: "http://box-a:3001", status: "connected" },
			{ hostId: "box-b", label: "Box B", url: "http://box-b:3001", status: "connected" },
		]}
		workspaces={[
			{ hostId: "box-a", id: "shared", name: "Shared", path: "/a", sessions: [] },
			{ hostId: "box-b", id: "shared", name: "Shared", path: "/b", sessions: [] },
		]}
		onOpenSession={vi.fn()}
		onOpenProject={vi.fn()}
		onOpenHome={vi.fn()}
		onNewTask={vi.fn()}
		onOrchestrator={vi.fn()}
		onConfigure={vi.fn()}
		onAddProject={vi.fn()}
		onRemoveProject={removeProject}
		onRetry={vi.fn()}
	/></SidebarMenu></SidebarProvider></TooltipProvider>);
	const boxBActions = screen.getByRole("button", { name: "Project actions for Shared on Box B" });
	await user.click(boxBActions);
	await user.click(await screen.findByRole("menuitem", { name: "Remove project" }));
	let dialog = await screen.findByRole("dialog", { name: "Remove project" });
	expect(dialog).toHaveTextContent("repository folder and stored history");
	await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
	expect(removeProject).not.toHaveBeenCalled();
	await user.click(boxBActions);
	await user.click(await screen.findByRole("menuitem", { name: "Remove project" }));
	dialog = await screen.findByRole("dialog", { name: "Remove project" });
	await user.click(within(dialog).getByRole("button", { name: "Remove" }));
	expect(removeProject).toHaveBeenCalledWith("box-b", "shared");
	expect(boxBActions).toBeDisabled();
	expect(screen.getByRole("button", { name: "Project actions for Shared on Box A" })).toBeEnabled();
	finishRemove();
	await waitFor(() => expect(boxBActions).toBeEnabled());
});

it("shows a failed removal beside the remote project", async () => {
	const user = userEvent.setup();
	render(<TooltipProvider><SidebarProvider><SidebarMenu><RemoteHostsSection
		hosts={[{ hostId: "box-b", label: "Box B", url: "http://box-b:3001", status: "connected" }]}
		workspaces={[{ hostId: "box-b", id: "project-1", name: "Agent Repo", path: "/remote", sessions: [] }]}
		onOpenSession={vi.fn()}
		onOpenProject={vi.fn()}
		onOpenHome={vi.fn()}
		onNewTask={vi.fn()}
		onOrchestrator={vi.fn()}
		onConfigure={vi.fn()}
		onAddProject={vi.fn()}
		onRemoveProject={vi.fn().mockRejectedValue(new Error("Host disconnected"))}
		onRetry={vi.fn()}
	/></SidebarMenu></SidebarProvider></TooltipProvider>);
	await user.click(screen.getByRole("button", { name: "Project actions for Agent Repo on Box B" }));
	await user.click(await screen.findByRole("menuitem", { name: "Remove project" }));
	await user.click(within(await screen.findByRole("dialog", { name: "Remove project" })).getByRole("button", { name: "Remove" }));
	const row = document.querySelector('[data-remote-project-row][data-host-id="box-b"]');
	expect(row).not.toBeNull();
	expect(await within(row as HTMLElement).findByRole("alert")).toHaveTextContent("Host disconnected");
});
