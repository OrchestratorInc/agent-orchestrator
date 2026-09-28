import { act, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

type Catalog = { authorized: { id: string; label: string }[]; installed: { id: string; label: string }[]; supported: { id: string; label: string }[] };
const mocks = vi.hoisted(() => ({ navigate: vi.fn(), requestFinish: vi.fn(), agents: {} as { data: Catalog | undefined; isFetching: boolean; isLoading: boolean } }));

vi.mock("@tanstack/react-router", async (original) => ({ ...(await original<typeof import("@tanstack/react-router")>()), useNavigate: () => mocks.navigate }));
vi.mock("../stores/ui-store", () => ({
	useResolvedTheme: () => "dark" as const,
	useUiStore: (select: (state: unknown) => unknown) => select({ requestOnboardingFinish: mocks.requestFinish, clearOnboardingFinishError: vi.fn(), onboardingFinishRequest: null, onboardingFinishError: null }),
}));
vi.mock("../hooks/useAgentsQuery", () => ({ refreshAgentsIfStale: vi.fn().mockResolvedValue(undefined), useAgentsQuery: () => mocks.agents }));
vi.mock("../components/OnboardingProjectSetup", () => ({
	OnboardingProjectSetup: ({ onPrepared }: { onPrepared: (input: { path: string }) => void }) => <button type="button" onClick={() => onPrepared({ path: "/tmp/acme/project" })}>Prepare project</button>,
}));
vi.mock("../lib/api-client", async (original) => {
	const actual = await original<typeof import("../lib/api-client")>();
	return { ...actual, apiClient: { ...actual.apiClient, GET: vi.fn(async (path: string) => {
		if (path === "/api/v1/system/requirements") return { data: { ready: true, requirements: [{ id: "gh", satisfied: true }] } };
		if (path === "/api/v1/system/github-auth") return { data: { satisfied: true } };
		return { data: undefined };
	}) } };
});

import { OnboardingPage } from "../components/OnboardingPage";

async function renderOnboarding() {
	await act(async () => render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><OnboardingPage /></QueryClientProvider>));
}
async function reachAgents(user: ReturnType<typeof userEvent.setup>) {
	await user.click(screen.getByRole("button", { name: "Continue" }));
	await user.click(await screen.findByRole("button", { name: "Proceed to setup" }));
	await user.click(await screen.findByRole("button", { name: "Continue" }));
	await user.click(await screen.findByRole("button", { name: "Continue" }));
	await user.click(await screen.findByRole("button", { name: "Prepare project" }));
	await screen.findByRole("heading", { name: "Pick your agents." });
}
async function choose(user: ReturnType<typeof userEvent.setup>, label: string, option: string) {
	await user.click(screen.getByRole("combobox", { name: label }));
	await user.click(await screen.findByRole("option", { name: option }));
}

beforeEach(() => {
	mocks.navigate.mockReset();
	mocks.requestFinish.mockReset();
	mocks.agents = { data: { authorized: [{ id: "claude-code", label: "Claude Code" }, { id: "codex", label: "Codex" }], installed: [{ id: "claude-code", label: "Claude Code" }, { id: "codex", label: "Codex" }], supported: [{ id: "claude-code", label: "Claude Code" }, { id: "codex", label: "Codex" }] }, isFetching: false, isLoading: false };
});

describe("onboarding route", () => {
	it("collects both roles in one step and preserves the existing finish handoff", async () => {
		const user = userEvent.setup();
		await renderOnboarding();
		await reachAgents(user);
		const next = screen.getByRole("button", { name: "See how it works" });
		expect(next).toBeDisabled();
		await choose(user, "Orchestrator agent", "Codex");
		await choose(user, "Worker agents", "Claude Code");
		expect(next).toBeEnabled();
		await user.click(next);
		await screen.findByRole("heading", { name: "Give your orchestrator a goal." });
		await user.click(screen.getByRole("button", { name: "Continue to orchestrator" }));
		expect(mocks.requestFinish).toHaveBeenCalledWith({ path: "/tmp/acme/project", orchestratorAgent: "codex", workerAgent: "claude-code" });
		expect(mocks.navigate).toHaveBeenCalledWith({ to: "/" });
	});

	it("holds the combined step when no harness is ready", async () => {
		mocks.agents = { data: { authorized: [], installed: [], supported: [{ id: "claude-code", label: "Claude Code" }] }, isFetching: false, isLoading: false };
		const user = userEvent.setup();
		await renderOnboarding();
		await reachAgents(user);
		expect(screen.getByRole("button", { name: "See how it works" })).toBeDisabled();
		expect(screen.getByText("Install and sign in to at least one agent to continue.")).toBeInTheDocument();
	});
});
