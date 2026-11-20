import { act, render, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { SessionDeepLinkEvent } from "../../main/session-deep-link";
import { useUiStore } from "../stores/ui-store";
import { SessionDeepLinkHost } from "./SessionDeepLinkHost";

const mocks = vi.hoisted(() => ({
	navigate: vi.fn(),
	workspace: vi.fn(),
	listener: undefined as undefined | ((link: SessionDeepLinkEvent | null) => void),
	pending: null as SessionDeepLinkEvent | null,
}));
vi.mock("../lib/navigate-to-session", () => ({ useNavigateToSession: () => mocks.navigate }));
vi.mock("../hooks/useWorkspaceQuery", () => ({ useWorkspaceQuery: () => mocks.workspace() }));
vi.mock("../lib/bridge", () => ({
	aoBridge: {
		sessionLinks: {
			onOpen: (listener: (link: SessionDeepLinkEvent | null) => void) => {
				mocks.listener = listener;
				return () => undefined;
			},
			takePending: async () => mocks.pending,
		},
	},
}));

const toastTitles = () => useUiStore.getState().globalToasts.map((toast) => toast.title);

describe("SessionDeepLinkHost", () => {
	beforeEach(() => {
		mocks.navigate.mockReset();
		mocks.pending = null;
		mocks.workspace.mockReturnValue({
			isSuccess: true,
			isError: false,
			data: [{ id: "project", sessions: [{ id: "live" }, { id: "gone", isTerminated: true }] }],
		});
		useUiStore.setState({ globalToast: null, globalToasts: [], globalToastSequence: 0 });
	});

	it("navigates to a live session link", async () => {
		render(<SessionDeepLinkHost />);
		act(() => mocks.listener?.({ url: "ao://sessions/project/live" }));
		await waitFor(() => expect(mocks.navigate).toHaveBeenCalledWith("project", "live"));
	});

	it("opens a terminated session's retained history", async () => {
		render(<SessionDeepLinkHost />);
		act(() => mocks.listener?.({ url: "ao://sessions/project/gone" }));
		await waitFor(() => expect(mocks.navigate).toHaveBeenCalledWith("project", "gone"));
	});

	it("navigates to a link queued before the renderer was ready", async () => {
		mocks.pending = { url: "ao://sessions/project/live" };
		render(<SessionDeepLinkHost />);
		await waitFor(() => expect(mocks.navigate).toHaveBeenCalledWith("project", "live"));
	});

	it("waits for the workspace list before resolving", async () => {
		mocks.workspace.mockReturnValue({ isSuccess: false, isError: false, data: undefined });
		const view = render(<SessionDeepLinkHost />);
		act(() => mocks.listener?.({ url: "ao://sessions/project/live" }));
		expect(mocks.navigate).not.toHaveBeenCalled();
		mocks.workspace.mockReturnValue({ isSuccess: true, isError: false, data: [{ id: "project", sessions: [{ id: "live" }] }] });
		view.rerender(<SessionDeepLinkHost />);
		await waitFor(() => expect(mocks.navigate).toHaveBeenCalledWith("project", "live"));
	});

	it("reports a missing target without navigating", async () => {
		render(<SessionDeepLinkHost />);
		act(() => mocks.listener?.({ url: "ao://sessions/project/nope" }));
		await waitFor(() => expect(toastTitles()).toEqual(["That session is missing or isn't available in this AO workspace."]));
		expect(mocks.navigate).not.toHaveBeenCalled();
	});

	it("reports malformed and unsupported links", async () => {
		render(<SessionDeepLinkHost />);
		act(() => mocks.listener?.({ error: "malformed" }));
		await waitFor(() => expect(toastTitles()).toContain("This AO link is malformed."));
		act(() => mocks.listener?.({ error: "unsupported" }));
		await waitFor(() => expect(toastTitles()).toContain("This AO link points to something AO can't open."));
		expect(mocks.navigate).not.toHaveBeenCalled();
	});
});
