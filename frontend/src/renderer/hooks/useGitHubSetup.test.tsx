import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";

const state = vi.hoisted(() => ({
	auth: { id: "github-auth", label: "GitHub access", satisfied: false, required: false, version: "2.3.0", updateRequired: true },
	pending: false,
	installing: false,
	job: undefined as undefined | { status: string; command?: string },
	afterInstall: (): void => undefined,
	install: vi.fn(),
	signIn: vi.fn(),
	refetchAuth: vi.fn(),
	refetchRequirements: vi.fn(),
}));

vi.mock("./useSystemRequirementsGate", () => ({
	useSystemRequirementsGate: () => ({ requirements: [{ id: "gh", satisfied: true }], query: { refetch: state.refetchRequirements } }),
	useGitHubAuthRequirement: () => ({ data: state.auth, isPending: state.pending, refetch: state.refetchAuth }),
	useGitHubDeviceLogin: () => ({ login: { state: "idle" }, start: { mutate: state.signIn }, cancel: { mutate: vi.fn() } }),
}));
vi.mock("../components/InstallDependencyDialog", async (original) => {
	const actual = await original<typeof import("../components/InstallDependencyDialog")>();
	return { ...actual, useInstallRunner: (onSucceeded: () => void) => {
		state.afterInstall = onSucceeded;
		return { start: state.install, running: state.installing, startError: undefined, jobFor: () => state.job };
	} };
});

import { OnboardingGitHubStep } from "../components/OnboardingGitHubStep";
import { useGitHubSetup } from "./useGitHubSetup";

function Screen() {
	const setup = useGitHubSetup();
	return <><OnboardingGitHubStep setup={setup} /><button disabled={!setup.authSatisfied}>Continue</button></>;
}

beforeEach(() => {
	state.auth = { id: "github-auth", label: "GitHub access", satisfied: false, required: false, version: "2.3.0", updateRequired: true };
	state.pending = false;
	state.installing = false;
	state.job = undefined;
	state.install.mockReset();
	state.signIn.mockReset();
	state.refetchAuth.mockReset().mockImplementation(async () => ({ data: state.auth, isError: false }));
	state.refetchRequirements.mockReset().mockResolvedValue({ data: { requirements: [{ id: "gh", satisfied: true }] } });
});

it("offers Update for an old CLI even when existing credentials are present", async () => {
	state.auth.satisfied = true;
	render(<Screen />);
	expect(screen.getByRole("button", { name: "Continue" })).toBeDisabled();
	expect(screen.queryByRole("button", { name: "Sign in with GitHub" })).not.toBeInTheDocument();
	expect(screen.getByText(/2.3.0 is older than 2.4.0/)).toBeVisible();
	await userEvent.click(screen.getByRole("button", { name: "Update GitHub CLI" }));
	expect(state.install).toHaveBeenCalledWith("gh");
});

it("waits for the first version probe before offering sign-in", () => {
	state.pending = true;
	render(<Screen />);
	expect(screen.getByRole("status")).toHaveTextContent("Checking availability");
	expect(screen.queryByRole("button", { name: "Update GitHub CLI" })).not.toBeInTheDocument();
});

it("prevents duplicate updates and shows manual instructions when root is required", () => {
	state.installing = true;
	const { rerender } = render(<Screen />);
	expect(screen.getByRole("button", { name: "Update GitHub CLI" })).toBeDisabled();
	state.installing = false;
	state.job = { status: "unsupported", command: "sudo apt-get update; sudo apt-get install --only-upgrade -y gh" };
	rerender(<Screen />);
	expect(screen.getByText(/sudo apt-get update/)).toBeVisible();
	expect(screen.getByRole("button", { name: "Download GitHub CLI" })).toBeVisible();
	expect(screen.getByRole("button", { name: "Continue" })).toBeDisabled();
});

it("rechecks the resolved version before starting sign-in after an update", async () => {
	const { rerender } = render(<Screen />);
	state.auth = { ...state.auth, version: "2.100.0", updateRequired: false };
	await act(async () => state.afterInstall());
	await waitFor(() => expect(state.signIn).toHaveBeenCalledOnce());
	expect(state.refetchRequirements).toHaveBeenCalledOnce();
	expect(state.refetchAuth).toHaveBeenCalledOnce();
	rerender(<Screen />);
	expect(screen.getByRole("button", { name: "Sign in with GitHub" })).toBeVisible();
});

it("keeps Update available when an old copy still shadows the updated CLI", async () => {
	render(<Screen />);
	await act(async () => state.afterInstall());
	expect(await screen.findByText(/2.3.0 is still on PATH/)).toBeVisible();
	expect(state.signIn).not.toHaveBeenCalled();
	expect(screen.getByRole("button", { name: "Update GitHub CLI" })).toBeEnabled();
});

it("does not start sign-in after failed verification or a failed update", async () => {
	state.refetchAuth.mockResolvedValue({ isError: true });
	const { rerender } = render(<Screen />);
	await act(async () => state.afterInstall());
	expect(await screen.findByText(/couldn't verify the GitHub CLI version/)).toBeVisible();
	expect(state.signIn).not.toHaveBeenCalled();
	state.job = { status: "failed" };
	rerender(<Screen />);
	expect(screen.getByRole("button", { name: "Update GitHub CLI" })).toBeEnabled();
});

it("allows the supported boundary version without an update", () => {
	state.auth = { ...state.auth, version: "2.4.0", updateRequired: false, satisfied: true };
	render(<Screen />);
	expect(screen.getByRole("button", { name: "Continue" })).toBeEnabled();
	expect(screen.queryByRole("button", { name: "Update GitHub CLI" })).not.toBeInTheDocument();
});
