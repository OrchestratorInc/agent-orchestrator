import { render, waitFor } from "@testing-library/react";
import { Suspense, type ComponentType } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { Route } from "./_shell.settings";

const mocks = vi.hoisted(() => ({ navigate: vi.fn(), search: {} as Record<string, unknown> }));

vi.mock("@tanstack/react-router", async (importOriginal) => ({
	...await importOriginal<typeof import("@tanstack/react-router")>(),
	createFileRoute: () => (options: Record<string, unknown>) => ({ options, useSearch: () => mocks.search }),
	useNavigate: () => mocks.navigate,
}));

beforeEach(() => {
	mocks.navigate.mockReset().mockResolvedValue(undefined);
	mocks.search = {};
	useUiStore.setState({ settingsModal: null });
});

async function renderPage() {
	const Page = Route.options.component as ComponentType & { preload?: () => Promise<unknown> };
	await Page.preload?.();
	render(<Suspense fallback={null}><Page /></Suspense>);
}

describe("legacy settings links", () => {
	it.each(["agents", "subscriptions", "accounts"])("opens Accounts from section=%s", async (section) => {
		const validate = Route.options.validateSearch as ((search: Record<string, unknown>) => Record<string, unknown>) | undefined;
		expect(validate).toBeTypeOf("function");
		mocks.search = validate!({ section });
		await renderPage();
		await waitFor(() => expect(useUiStore.getState().settingsModal).toEqual({ scope: "global", section: "accounts" }));
		expect(mocks.navigate).toHaveBeenCalledWith({ to: "/", replace: true });
	});

	it.each(["unknown", ["agents"], { section: "agents" }, undefined])("rejects invalid section values without a dead end: %s", async (section) => {
		const validate = Route.options.validateSearch as ((search: Record<string, unknown>) => Record<string, unknown>) | undefined;
		expect(validate).toBeTypeOf("function");
		mocks.search = validate!({ section });
		await renderPage();
		await waitFor(() => expect(useUiStore.getState().settingsModal).toEqual({ scope: "global", section: undefined }));
		expect(mocks.navigate).toHaveBeenCalledWith({ to: "/", replace: true });
	});

	it("retains valid non-account settings links", async () => {
		const validate = Route.options.validateSearch as (search: Record<string, unknown>) => Record<string, unknown>;
		mocks.search = validate({ section: "harness" });
		await renderPage();
		await waitFor(() => expect(useUiStore.getState().settingsModal).toEqual({ scope: "global", section: "harness" }));
	});
});
