import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";

const state = vi.hoisted(() => ({
	auth: { id: "github-auth", satisfied: false, version: "2.3.0", updateRequired: true },
	startLogin: vi.fn(),
	update: vi.fn(),
	job: undefined as undefined | { status: string; command: string },
}));

vi.mock("../hooks/useSystemRequirementsGate", () => ({
	useSystemRequirementsGate: () => ({ requirements: [{ id: "gh", satisfied: true }] }),
	useGitHubAuthRequirement: () => ({ data: state.auth, refetch: vi.fn() }),
	useStartGitHubAuthTerminal: () => ({ mutate: state.startLogin, isPending: false }),
	useGitHubAuthAutoLoginOffered: () => ({ offered: false, markOffered: vi.fn() }),
	useGitHubAuthTerminal: () => ({ data: null, clear: vi.fn() }),
}));
vi.mock("../hooks/useShellTerminals", () => ({ useCloseShellTerminal: () => ({ mutate: vi.fn() }) }));
vi.mock("../lib/shell-context", () => ({ useShellMaybe: () => null }));
vi.mock("../lib/onboarding-telemetry", () => ({ useOnboardingStep: vi.fn() }));
vi.mock("../stores/ui-store", () => ({
	useResolvedTheme: () => "dark",
	useUiStore: (select: (state: unknown) => unknown) => select({ showGlobalToast: vi.fn() }),
}));
vi.mock("./InstallDependencyDialog", async (original) => ({
	...await original<typeof import("./InstallDependencyDialog")>(),
	useInstallRunner: () => ({ start: state.update, running: false, jobFor: () => state.job }),
}));
vi.mock("./TerminalPane", () => ({ TerminalPane: () => null }));

import { GitHubOnboardingNotice } from "./GitHubOnboardingNotice";

beforeEach(() => {
	state.auth = { id: "github-auth", satisfied: false, version: "2.3.0", updateRequired: true };
	state.job = undefined;
	state.startLogin.mockReset();
	state.update.mockReset();
});

it("offers Update instead of automatically starting a terminal with an old CLI", async () => {
	render(<GitHubOnboardingNotice />);
	expect(state.startLogin).not.toHaveBeenCalled();
	expect(screen.queryByRole("button", { name: "Sign in with GitHub" })).not.toBeInTheDocument();
	await userEvent.click(screen.getByRole("button", { name: "Update GitHub CLI" }));
	expect(state.update).toHaveBeenCalledWith("gh");
});

it("shows manual instructions for an update that requires administrator access", () => {
	state.job = { status: "unsupported", command: "sudo apt-get install --only-upgrade -y gh" };
	render(<GitHubOnboardingNotice />);
	expect(screen.getByText(/sudo apt-get install/)).toBeVisible();
	expect(screen.getByRole("button", { name: "Download GitHub CLI" })).toBeVisible();
	expect(state.startLogin).not.toHaveBeenCalled();
});

it("preserves the existing automatic sign-in offer for a supported version", () => {
	state.auth = { ...state.auth, version: "2.4.0", updateRequired: false };
	render(<GitHubOnboardingNotice />);
	expect(state.startLogin).toHaveBeenCalledOnce();
	expect(screen.queryByRole("button", { name: "Update GitHub CLI" })).not.toBeInTheDocument();
});
