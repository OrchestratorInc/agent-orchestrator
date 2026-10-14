import { act, render } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { STANDALONE_WORKSPACE_ID } from "../types/workspace";
import { NewTaskNavigator } from "./NewTaskNavigator";

const { navigateMock } = vi.hoisted(() => ({ navigateMock: vi.fn() }));

vi.mock("@tanstack/react-router", () => ({
	useNavigate: () => navigateMock,
}));

beforeEach(() => {
	navigateMock.mockReset();
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
			{ to: "/new", search: { project: "proj-1", host: undefined } },
			{ to: "/new", search: { project: "proj-1", host: undefined } },
			{ to: "/new", search: { project: "proj-2", host: "box-a" } },
			{ to: "/new", search: { project: undefined, host: undefined } },
		]);
	});

	it("does not replay a request raised before it mounted", () => {
		act(() => useUiStore.getState().requestNewTask("proj-1"));
		render(<NewTaskNavigator />);
		expect(navigateMock).not.toHaveBeenCalled();
	});
});
