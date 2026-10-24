import { act, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Suspense, type ComponentType } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ONBOARDING_COMPLETE_STORAGE_KEY } from "../lib/onboarding-finish";
import { Route } from "./_shell.index";

const state = vi.hoisted(() => ({
	navigate: vi.fn(),
	preview: false,
	settingsModal: null as null | { scope: string },
	query: { isSuccess: true, data: [] as { kind?: string }[] },
	cloudSession: vi.fn(),
	remotes: vi.fn(),
}));
vi.mock("@tanstack/react-router", async (original) => ({
	...(await original<typeof import("@tanstack/react-router")>()),
	useNavigate: () => state.navigate,
}));
vi.mock("../hooks/useWorkspaceQuery", () => ({ useWorkspaceQuery: () => state.query }));
vi.mock("../lib/bridge", () => ({ aoBridge: { cloud: { getSession: state.cloudSession }, remotes: { list: state.remotes } } }));
vi.mock("../lib/preview-mode", () => ({ get usesPreviewWorkspaceData() { return state.preview; } }));
vi.mock("../stores/ui-store", () => ({ useUiStore: (select: (value: { settingsModal: typeof state.settingsModal }) => unknown) => select({ settingsModal: state.settingsModal }) }));
vi.mock("../components/HomePage", () => ({ HomePage: () => <div>Home</div> }));
vi.mock("../components/MigrationPopup", () => ({ MigrationPopup: () => null }));
const HomeRoute = Route.options.component as ComponentType;
async function renderHome() {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const view = await act(async () => render(<Suspense fallback={null}><HomeRoute /></Suspense>, {
		wrapper: ({ children }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>,
	}));
	await screen.findByText("Home");
	return { ...view, queryClient };
}

beforeEach(() => {
	window.localStorage.clear();
	state.navigate.mockReset();
	state.preview = false;
	state.settingsModal = null;
	state.query = { isSuccess: true, data: [] };
	state.cloudSession.mockReset().mockResolvedValue(null);
	state.remotes.mockReset().mockResolvedValue([]);
});

describe("onboarding entry for existing profiles", () => {
	it.each(["single_repo", "workspace", "standalone", "cloud", undefined])("keeps a profile with an existing %s workspace in the app", async (kind) => {
		state.query.data = [{ kind }];
		await renderHome();
		expect(state.navigate).not.toHaveBeenCalled();
		expect(window.localStorage.getItem(ONBOARDING_COMPLETE_STORAGE_KEY)).toBe("1");
		expect(state.cloudSession).not.toHaveBeenCalled();
		expect(state.remotes).not.toHaveBeenCalled();
	});

	it.each([{ data: [] }, { data: [{ kind: "scratch" }] }])("opens onboarding for an incomplete empty or scratch-only profile: %j", async ({ data }) => {
		state.query.data = data;
		await renderHome();
		await waitFor(() => expect(state.navigate).toHaveBeenCalledWith({ to: "/onboarding", replace: true }));
	});

	it("keeps a completed profile in the app even after all projects are removed", async () => {
		window.localStorage.setItem(ONBOARDING_COMPLETE_STORAGE_KEY, "1");
		await renderHome();
		expect(state.navigate).not.toHaveBeenCalled();
		expect(state.cloudSession).not.toHaveBeenCalled();
		expect(state.remotes).not.toHaveBeenCalled();
	});

	it("waits for a successful workspace load before deciding this is first run", async () => {
		state.query.isSuccess = false;
		const view = await renderHome();
		expect(state.navigate).not.toHaveBeenCalled();
		state.query = { isSuccess: true, data: [{ kind: "single_repo" }] };
		await act(async () => view.rerender(<HomeRoute />));
		expect(state.navigate).not.toHaveBeenCalled();
		expect(window.localStorage.getItem(ONBOARDING_COMPLETE_STORAGE_KEY)).toBe("1");
	});

	it("defers the decision while settings are open", async () => {
		state.settingsModal = { scope: "global" };
		const view = await renderHome();
		expect(state.navigate).not.toHaveBeenCalled();
		state.settingsModal = null;
		await act(async () => view.rerender(<HomeRoute />));
		await waitFor(() => expect(state.navigate).toHaveBeenCalledWith({ to: "/onboarding", replace: true }));
	});

	it("keeps a cloud-only profile in the app before its cloud projects load", async () => {
		state.cloudSession.mockResolvedValue({ user: { id: "existing-cloud-user" } });
		await renderHome();
		await waitFor(() => expect(window.localStorage.getItem(ONBOARDING_COMPLETE_STORAGE_KEY)).toBe("1"));
		expect(state.navigate).not.toHaveBeenCalled();
	});

	it("keeps a remote-only profile in the app even when its saved host is offline", async () => {
		state.remotes.mockResolvedValue([{ hostId: "remote-box", status: "offline" }]);
		await renderHome();
		await waitFor(() => expect(window.localStorage.getItem(ONBOARDING_COMPLETE_STORAGE_KEY)).toBe("1"));
		expect(state.navigate).not.toHaveBeenCalled();
	});

	it.each(["cloudSession", "remotes"] as const)("waits for the %s profile read before deciding this is first run", async (source) => {
		let resolve!: (value: unknown) => void;
		state[source].mockReturnValue(new Promise((done) => { resolve = done; }));
		await renderHome();
		expect(state.navigate).not.toHaveBeenCalled();
		await act(async () => resolve(source === "cloudSession" ? null : []));
		await waitFor(() => expect(state.navigate).toHaveBeenCalledWith({ to: "/onboarding", replace: true }));
	});

	it.each(["cloudSession", "remotes"] as const)("does not force onboarding when the %s profile read fails", async (source) => {
		state[source].mockRejectedValue(new Error("profile unavailable"));
		const view = await renderHome();
		await waitFor(() => expect(state[source]).toHaveBeenCalledOnce());
		await waitFor(() => expect(view.queryClient.isFetching()).toBe(0));
		expect(state.navigate).not.toHaveBeenCalled();
		expect(window.localStorage.getItem(ONBOARDING_COMPLETE_STORAGE_KEY)).toBeNull();
	});

	it.each(["cloudSession", "remotes"] as const)("keeps an existing profile in the app when %s resolves late", async (source) => {
		let resolve!: (value: unknown) => void;
		state[source].mockReturnValue(new Promise((done) => { resolve = done; }));
		await renderHome();
		expect(state.navigate).not.toHaveBeenCalled();
		await act(async () => resolve(source === "cloudSession" ? { user: { id: "cloud-user" } } : [{ hostId: "saved-host" }]));
		await waitFor(() => expect(window.localStorage.getItem(ONBOARDING_COMPLETE_STORAGE_KEY)).toBe("1"));
		expect(state.navigate).not.toHaveBeenCalled();
	});

	it("does not redirect if a local project arrives while the profile check is pending", async () => {
		let resolve!: (value: null) => void;
		state.cloudSession.mockReturnValue(new Promise((done) => { resolve = done; }));
		const view = await renderHome();
		state.query.data = [{ kind: "single_repo" }];
		await act(async () => view.rerender(<HomeRoute />));
		await act(async () => resolve(null));
		await waitFor(() => expect(view.queryClient.isFetching()).toBe(0));
		expect(state.navigate).not.toHaveBeenCalled();
		expect(window.localStorage.getItem(ONBOARDING_COMPLETE_STORAGE_KEY)).toBe("1");
	});

	it("does not redirect preview mode", async () => {
		state.preview = true;
		await renderHome();
		expect(state.navigate).not.toHaveBeenCalled();
	});
});
