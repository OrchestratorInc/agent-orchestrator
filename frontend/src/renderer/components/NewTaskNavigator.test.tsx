import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { CLOUD_PROJECT_KIND, STANDALONE_WORKSPACE_ID } from "../types/workspace";
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
	workspaceQueryKeyForHost: () => ["workspaces"],
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

	it("keeps the New Task dialog for cloud projects", () => {
		renderNavigator();
		act(() => useUiStore.getState().requestNewTask("cloud-1"));
		expect(navigateMock).not.toHaveBeenCalled();
		expect(dialog.props).toMatchObject({ open: true, projectId: "cloud-1" });
	});

	it("does not replay a request raised before it mounted", () => {
		act(() => useUiStore.getState().requestNewTask("proj-1"));
		renderNavigator();
		expect(navigateMock).not.toHaveBeenCalled();
	});
});
