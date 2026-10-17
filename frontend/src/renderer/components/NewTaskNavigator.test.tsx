import { act, render } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { STANDALONE_WORKSPACE_ID } from "../types/workspace";
import { NewTaskNavigator } from "./NewTaskNavigator";

const { navigateMock, route } = vi.hoisted(() => ({ navigateMock: vi.fn(), route: { onNewChat: false } }));

vi.mock("@tanstack/react-router", () => ({
	useNavigate: () => navigateMock,
	useMatchRoute: () => ({ to }: { to: string }) => to === "/new" && route.onNewChat,
}));

beforeEach(() => {
	navigateMock.mockReset();
	route.onNewChat = false;
	useUiStore.setState({ newTaskRequest: null, provisioningProjectIds: new Set() });
});

describe("NewTaskNavigator", () => {
	it("opens the new chat page for each request, including a repeat for the same project", () => {
		render(<NewTaskNavigator />);
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
		render(<NewTaskNavigator />);
		act(() => useUiStore.getState().requestNewTask("proj-1"));
		expect(navigateMock).toHaveBeenCalledWith({ to: "/new", search: { project: "proj-1", host: undefined }, replace: true });
	});

	it("does not replay a request raised before it mounted", () => {
		act(() => useUiStore.getState().requestNewTask("proj-1"));
		render(<NewTaskNavigator />);
		expect(navigateMock).not.toHaveBeenCalled();
	});
});
