import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { CLOUD_PROJECT_KIND, CLOUD_STANDALONE_WORKSPACE_ID, STANDALONE_WORKSPACE_ID } from "../types/workspace";
import { NewTaskNavigator } from "./NewTaskNavigator";

const { navigateMock, route, dialog } = vi.hoisted(() => ({
	navigateMock: vi.fn(),
	route: { onNewChat: false },
	dialog: { props: undefined as { open: boolean; projectId?: string } | undefined },
}));

vi.mock("@tanstack/react-router", () => ({
	useNavigate: () => navigateMock,
	useMatchRoute: () => ({ to }: { to: string }) => to === "/new" && route.onNewChat,
}));
vi.mock("../hooks/useWorkspaceQuery", () => ({
	useWorkspaceQuery: () => ({ data: [
		{ id: "proj-1", kind: "single_repo", sessions: [] },
		{ id: "cloud-1", kind: CLOUD_PROJECT_KIND, sessions: [] },
	] }),
	useRemoteWorkspaces: () => ({ data: [] }),
	useCloudProjectsQuery: () => ({ data: [
		{ id: "cloud-1", config: { sandboxProvider: "freestyle" } },
		{ id: "coder-1", config: { sandboxProvider: "coder" } },
		// A standalone agent's project on an organization that defaults to Coder.
		{ id: "agent-1", config: {}, standaloneAgent: true },
	] }),
	useSharedCloudWorkspacesQuery: () => ({ data: [
		{ id: "shared-1", kind: CLOUD_PROJECT_KIND, sessions: [], shared: { source: "link" } },
		{ id: "agent-shared", kind: CLOUD_PROJECT_KIND, sessions: [], shared: { source: "link" }, standaloneAgent: true },
	] }),
	workspaceQueryKeyForHost: () => ["workspaces"],
}));
vi.mock("../hooks/useCloudOffering", () => ({ useCloudOffering: () => ({ defaultProvider: "freestyle", loaded: true }) }));
vi.mock("../lib/cloud-offering", async (importOriginal) => ({
	...(await importOriginal<typeof import("../lib/cloud-offering")>()),
	projectSandboxProvider: (config: { sandboxProvider?: string } | undefined) => config?.sandboxProvider ?? "coder",
}));
vi.mock("./NewTaskDialog", () => ({
	NewTaskDialog: (props: { open: boolean; projectId?: string }) => {
		dialog.props = props;
		return null;
	},
}));

function renderNavigator() {
	render(<QueryClientProvider client={new QueryClient()}><NewTaskNavigator /></QueryClientProvider>);
}

beforeEach(() => {
	navigateMock.mockReset();
	route.onNewChat = false;
	dialog.props = undefined;
	useUiStore.setState({ newTaskRequest: null, provisioningProjectIds: new Set() });
});

describe("NewTaskNavigator", () => {
	it("opens the new chat page for each request, including a repeat for the same project", () => {
		renderNavigator();
		expect(navigateMock).not.toHaveBeenCalled();

		act(() => useUiStore.getState().requestNewTask("proj-1"));
		act(() => useUiStore.getState().requestNewTask("proj-1"));
		act(() => useUiStore.getState().requestNewTask("proj-2", "box-a"));
		act(() => useUiStore.getState().requestNewTask(STANDALONE_WORKSPACE_ID));

		expect(navigateMock.mock.calls.map(([target]) => target)).toEqual([
			{ to: "/new", search: { project: "proj-1", host: undefined }, replace: false },
			{ to: "/new", search: { project: "proj-1", host: undefined }, replace: false },
			{ to: "/new", search: { project: "proj-2", host: "box-a" }, replace: false },
			{ to: "/new", search: { project: undefined, host: undefined }, replace: false },
		]);
	});

	it("replaces the new chat page rather than stacking another one", () => {
		route.onNewChat = true;
		renderNavigator();
		act(() => useUiStore.getState().requestNewTask("proj-1"));
		expect(navigateMock).toHaveBeenCalledWith({ to: "/new", search: { project: "proj-1", host: undefined }, replace: true });
	});

	it("starts another Cloud standalone agent for a new task beside one, never a second session in it", () => {
		renderNavigator();
		act(() => useUiStore.getState().requestNewTask("agent-1"));
		expect(navigateMock).toHaveBeenCalledWith(expect.objectContaining({ to: "/new", search: { project: CLOUD_STANDALONE_WORKSPACE_ID, host: undefined } }));
		expect(dialog.props).toMatchObject({ open: false });
	});

	it("starts a new task beside a standalone agent shared with the person outside it, and in a shared project as before", () => {
		renderNavigator();
		act(() => useUiStore.getState().requestNewTask("agent-shared"));
		expect(navigateMock).toHaveBeenLastCalledWith(expect.objectContaining({ to: "/new", search: { project: undefined, host: undefined } }));
		act(() => useUiStore.getState().requestNewTask("shared-1"));
		expect(navigateMock).toHaveBeenLastCalledWith(expect.objectContaining({ to: "/new", search: { project: "shared-1", host: undefined } }));
	});

	it("opens the new chat page for cloud projects too", () => {
		renderNavigator();
		act(() => useUiStore.getState().requestNewTask("cloud-1"));
		expect(navigateMock).toHaveBeenCalledWith(expect.objectContaining({ to: "/new", search: { project: "cloud-1", host: undefined } }));
		expect(dialog.props).toMatchObject({ open: false });
	});

	it("keeps the New Task dialog for a Coder project", () => {
		renderNavigator();
		act(() => useUiStore.getState().requestNewTask("coder-1"));
		expect(navigateMock).not.toHaveBeenCalled();
		expect(dialog.props).toMatchObject({ open: true, projectId: "coder-1" });
	});

	it("does not replay a request raised before it mounted", () => {
		act(() => useUiStore.getState().requestNewTask("proj-1"));
		renderNavigator();
		expect(navigateMock).not.toHaveBeenCalled();
	});
});
