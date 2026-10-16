import { act, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

type Catalog = { authorized: { id: string; label: string }[]; installed: { id: string; label: string }[]; supported: { id: string; label: string }[] };
const mocks = vi.hoisted(() => ({
	navigate: vi.fn(),
	requestFinish: vi.fn(),
	githubWorkflowActive: false,
	finishRequest: null as null | { nonce: number; path: string; orchestratorAgent: string; workerAgent: string },
	finishError: null as null | { nonce: number; message: string },
	agents: {} as { data: Catalog | undefined; isFetching: boolean; isLoading: boolean },
}));

vi.mock("@tanstack/react-router", async (original) => ({ ...(await original<typeof import("@tanstack/react-router")>()), useNavigate: () => mocks.navigate }));
vi.mock("../stores/ui-store", () => ({
	useResolvedTheme: () => "dark" as const,
	useUiStore: (select: (state: unknown) => unknown) => select({ requestOnboardingFinish: mocks.requestFinish, clearOnboardingFinishError: vi.fn(), onboardingFinishRequest: mocks.finishRequest, onboardingFinishError: mocks.finishError }),
}));
vi.mock("../hooks/useAgentsQuery", () => ({ refreshAgentsIfStale: vi.fn().mockResolvedValue(undefined), useAgentsQuery: () => mocks.agents }));
vi.mock("../components/OnboardingProjectSetup", () => ({
	OnboardingProjectSetup: ({ cloudAvailable, onPrepared }: { cloudAvailable?: boolean; onPrepared: (input: { path: string }) => void }) => <button type="button" data-cloud-available={String(cloudAvailable)} onClick={() => onPrepared({ path: "/tmp/acme/project" })}>Prepare project</button>,
}));
vi.mock("../components/AuthTerminalPanel", () => ({
	AuthTerminalPanel: ({ testId }: { testId?: string }) => <div data-testid={testId ?? "auth-terminal"} />,
}));
vi.mock("../hooks/useGitHubSetup", () => ({
	useGitHubSetup: () => ({
		authSatisfied: !mocks.githubWorkflowActive,
		cancelSignIn: vi.fn(),
		cliMissing: false,
		gh: { id: "gh", satisfied: true },
		install: vi.fn(),
		installError: null,
		installing: false,
		job: undefined,
		login: mocks.githubWorkflowActive
			? { id: "attempt-1", state: "awaiting_approval", userCode: "ABCD-1234", verificationUrl: "https://github.com/login/device" }
			: { state: "idle" },
		requirementsQuery: {},
		signIn: vi.fn(),
		signInActive: mocks.githubWorkflowActive,
		signInError: null,
		signInPending: false,
	}),
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
async function prepareProject(user: ReturnType<typeof userEvent.setup>) {
	await user.click(screen.getByRole("button", { name: "Continue" }));
	await user.click(await screen.findByRole("button", { name: "Continue" }));
	await user.click(await screen.findByRole("button", { name: "Prepare project" }));
}
async function reachAgents(user: ReturnType<typeof userEvent.setup>) {
	await prepareProject(user);
	await screen.findByRole("heading", { name: "Pick your agent" });
}
async function choose(user: ReturnType<typeof userEvent.setup>, label: string, option: string) {
	await user.click(screen.getByRole("combobox", { name: label }));
	await user.click(await screen.findByRole("option", { name: option }));
}

beforeEach(() => {
	mocks.navigate.mockReset();
	mocks.requestFinish.mockReset();
	mocks.githubWorkflowActive = false;
	mocks.finishRequest = null;
	mocks.finishError = null;
	mocks.agents = { data: { authorized: [{ id: "claude-code", label: "Claude Code" }, { id: "codex", label: "Codex" }], installed: [{ id: "claude-code", label: "Claude Code" }, { id: "codex", label: "Codex" }], supported: [{ id: "claude-code", label: "Claude Code" }, { id: "codex", label: "Codex" }] }, isFetching: false, isLoading: false };
});

describe("onboarding route", () => {
	it("shows the GitHub device code in the page and hides Back while sign-in is active", async () => {
		mocks.githubWorkflowActive = true;
		const user = userEvent.setup();
		await renderOnboarding();
		await user.click(screen.getByRole("button", { name: "Continue" }));

		expect(await screen.findByTestId("github-device-code")).toHaveTextContent("ABCD-1234");
		expect(screen.getByRole("button", { name: "Open GitHub" })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Back" })).not.toBeInTheDocument();
	});

	it("finishes directly from agent selection and applies the agent to both project roles", async () => {
		const user = userEvent.setup();
		await renderOnboarding();
		await reachAgents(user);
		expect(screen.getByText("Choose one coding agent for now. You can select separate orchestrator and worker agents later in project settings")).toBeInTheDocument();
		const next = screen.getByRole("button", { name: "Finish setup" });
		expect(next).toBeDisabled();
		await choose(user, "Agent", "Codex");
		expect(next).toBeEnabled();
		await user.click(next);
		expect(screen.queryByRole("heading", { name: "Give your orchestrator a goal" })).not.toBeInTheDocument();
		expect(mocks.requestFinish).toHaveBeenCalledWith({ path: "/tmp/acme/project", orchestratorAgent: "codex", workerAgent: "codex" });
		expect(mocks.navigate).toHaveBeenCalledWith({ to: "/" });
	});

	it("restores a failed handoff on agent selection so setup can be retried", async () => {
		mocks.finishRequest = { nonce: 1, path: "/tmp/acme/project", orchestratorAgent: "codex", workerAgent: "codex" };
		mocks.finishError = { nonce: 1, message: "Project could not be registered" };
		const user = userEvent.setup();
		await renderOnboarding();
		expect(await screen.findByRole("heading", { name: "Pick your agent" })).toBeInTheDocument();
		expect(screen.getByRole("combobox", { name: "Agent" })).toHaveTextContent("Codex");
		expect(screen.getByRole("alert")).toHaveTextContent("Project could not be registered");
		await user.click(screen.getByRole("button", { name: "Finish setup" }));
		expect(mocks.requestFinish).toHaveBeenCalledWith(expect.objectContaining({ path: "/tmp/acme/project", orchestratorAgent: "codex", workerAgent: "codex" }));
		expect(mocks.navigate).toHaveBeenCalledWith({ to: "/" });
	});

	it("lets a user install another agent and return to their selection", async () => {
		const user = userEvent.setup();
		await renderOnboarding();
		await reachAgents(user);
		await choose(user, "Agent", "Codex");

		await user.click(screen.getByRole("combobox", { name: "Agent" }));
		const installAnother = await screen.findByRole("option", { name: "Install another agent" });
		expect(screen.getAllByRole("option").at(-1)).toBe(installAnother);
		await user.click(installAnother);

		expect(await screen.findByRole("heading", { name: "Install another agent" })).toBeInTheDocument();
		expect(screen.getByText("Install another coding agent or sign in to one already installed")).toBeInTheDocument();
		const returnToAgents = screen.getByRole("button", { name: "Back to agents" });
		expect(returnToAgents).toBeEnabled();
		await user.click(returnToAgents);

		expect(await screen.findByRole("heading", { name: "Pick your agent" })).toBeInTheDocument();
		expect(screen.getByRole("combobox", { name: "Agent" })).toHaveTextContent("Codex");
	});

	it("keeps cloud onboarding disabled", async () => {
		const user = userEvent.setup();
		await renderOnboarding();
		await user.click(screen.getByRole("button", { name: "Continue" }));
		await user.click(await screen.findByRole("button", { name: "Continue" }));
		expect(await screen.findByRole("button", { name: "Prepare project" })).toHaveAttribute("data-cloud-available", "false");
		expect(screen.queryByRole("heading", { name: "Run sessions in the cloud" })).not.toBeInTheDocument();
	});

	it("inserts a required setup step before role selection when no harness is ready", async () => {
		mocks.agents = { data: { authorized: [], installed: [], supported: [{ id: "claude-code", label: "Claude Code" }] }, isFetching: false, isLoading: false };
		const user = userEvent.setup();
		await renderOnboarding();
		await prepareProject(user);
		expect(await screen.findByRole("heading", { name: "Install an agent" })).toBeInTheDocument();
		expect(screen.queryByRole("heading", { name: "Pick your agent" })).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Choose agents" })).toBeDisabled();
		expect(screen.getByRole("button", { name: "Install Claude Code" })).toBeInTheDocument();
	});
});
