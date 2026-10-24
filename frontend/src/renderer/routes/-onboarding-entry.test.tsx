import { act, render, screen } from "@testing-library/react";
import { Suspense, type ComponentType } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ONBOARDING_COMPLETE_STORAGE_KEY } from "../lib/onboarding-finish";
import { Route } from "./_shell.index";

const state = vi.hoisted(() => ({
	navigate: vi.fn(),
	preview: false,
	settingsModal: null as null | { scope: string },
	query: { isSuccess: true, data: [] as { kind?: string }[] },
}));
vi.mock("@tanstack/react-router", async (original) => ({
	...(await original<typeof import("@tanstack/react-router")>()),
	useNavigate: () => state.navigate,
}));
vi.mock("../hooks/useWorkspaceQuery", () => ({ useWorkspaceQuery: () => state.query }));
vi.mock("../lib/preview-mode", () => ({ get usesPreviewWorkspaceData() { return state.preview; } }));
vi.mock("../stores/ui-store", () => ({ useUiStore: (select: (value: { settingsModal: typeof state.settingsModal }) => unknown) => select({ settingsModal: state.settingsModal }) }));
vi.mock("../components/HomePage", () => ({ HomePage: () => <div>Home</div> }));
vi.mock("../components/MigrationPopup", () => ({ MigrationPopup: () => null }));
const HomeRoute = Route.options.component as ComponentType;
async function renderHome() {
	const view = await act(async () => render(<Suspense fallback={null}><HomeRoute /></Suspense>));
	await screen.findByText("Home");
	return view;
}

beforeEach(() => {
	window.localStorage.clear();
	state.navigate.mockReset();
	state.preview = false;
	state.settingsModal = null;
	state.query = { isSuccess: true, data: [] };
});

describe("onboarding entry for existing profiles", () => {
	it.each(["single_repo", "workspace", "standalone", "cloud", undefined])("keeps a profile with an existing %s workspace in the app", async (kind) => {
		state.query.data = [{ kind }];
		await renderHome();
		expect(state.navigate).not.toHaveBeenCalled();
		expect(window.localStorage.getItem(ONBOARDING_COMPLETE_STORAGE_KEY)).toBe("1");
	});

	it.each([{ data: [] }, { data: [{ kind: "scratch" }] }])("opens onboarding for an incomplete empty or scratch-only profile: %j", async ({ data }) => {
		state.query.data = data;
		await renderHome();
		expect(state.navigate).toHaveBeenCalledWith({ to: "/onboarding", replace: true });
	});

	it("keeps a completed profile in the app even after all projects are removed", async () => {
		window.localStorage.setItem(ONBOARDING_COMPLETE_STORAGE_KEY, "1");
		await renderHome();
		expect(state.navigate).not.toHaveBeenCalled();
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
		expect(state.navigate).toHaveBeenCalledWith({ to: "/onboarding", replace: true });
	});

	it("does not redirect preview mode", async () => {
		state.preview = true;
		await renderHome();
		expect(state.navigate).not.toHaveBeenCalled();
	});
});
