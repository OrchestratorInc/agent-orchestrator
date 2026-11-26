import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const capture = vi.hoisted(() => vi.fn(async (_event: string, _props?: unknown) => true));
const auth = vi.hoisted(() => ({ current: { isSuccess: true, isError: false, data: { satisfied: false } as { satisfied: boolean } | undefined, refetch: vi.fn() } }));
const login = vi.hoisted(() => ({ isError: false }));
const sentryFailure = vi.hoisted(() => vi.fn());

vi.mock("../lib/telemetry", () => ({ captureRendererEvent: capture, captureRendererEventOnUnload: vi.fn() }));
vi.mock("../lib/sentry", async (importOriginal) => ({
	...(await importOriginal<typeof import("../lib/sentry")>()),
	captureOnboardingFailureToSentry: sentryFailure,
}));
vi.mock("../hooks/useSystemRequirementsGate", () => ({
	useSystemRequirementsGate: () => ({ query: { refetch: vi.fn(), isSuccess: true, isError: false }, requirements: [{ id: "gh", satisfied: true }] }),
	useGitHubAuthRequirement: () => auth.current,
	useStartGitHubAuthTerminal: () => ({ mutate: vi.fn(), isPending: false, isError: login.isError, error: login.isError ? new Error("Login could not start") : null }),
	useGitHubAuthTerminal: () => ({ data: null, clear: vi.fn() }),
}));
vi.mock("../hooks/useShellTerminals", () => ({ useCloseShellTerminal: () => ({ mutate: vi.fn() }) }));
vi.mock("./InstallDependencyDialog", () => ({
	InstallAction: () => null,
	useInstallRunner: () => ({ inspect: vi.fn(), running: false, jobFor: () => undefined, inspectionFinished: () => false, target: null, start: vi.fn() }),
}));
vi.mock("./GitHubRepoPicker", () => ({ GitHubRepoPicker: () => null, preloadGitHubRepos: vi.fn() }));
vi.mock("./TerminalPane", () => ({ TerminalPane: () => null }));

import { onboardingActionWatched, resetOnboardingTelemetryForTests, setOnboardingTourStep, startOnboardingAction } from "../lib/onboarding-telemetry";
import { GitHubSetupPanel } from "./FirstRunSteps";

describe("GitHub onboarding step telemetry", () => {
	beforeEach(() => {
		capture.mockClear();
		sentryFailure.mockClear();
		login.isError = false;
		window.localStorage.clear();
		resetOnboardingTelemetryForTests();
	});

	it("reports github_auth viewed while sign-in is needed and completed once signed in", () => {
		// The clone dialog opened from the tour's project step.
		setOnboardingTourStep("tour_project");
		auth.current = { ...auth.current, data: { satisfied: false } };
		const onReady = vi.fn();
		const { rerender } = render(<GitHubSetupPanel onReady={onReady} />);
		auth.current = { ...auth.current, data: { satisfied: true } };
		rerender(<GitHubSetupPanel onReady={onReady} />);
		expect(capture.mock.calls).toEqual([
			["ao.onboarding.step_viewed", { run_kind: "first_run", step: "github_auth" }],
			["ao.onboarding.step_completed", { run_kind: "first_run", step: "github_auth", time_on_step_ms: expect.any(Number) }],
		]);
		expect(onReady).toHaveBeenCalled();
	});

	it("reports the sign-in it starts during onboarding as a gh_sign_in action", () => {
		setOnboardingTourStep("tour_project");
		auth.current = { ...auth.current, data: { satisfied: false } };
		const { rerender } = render(<GitHubSetupPanel onReady={vi.fn()} />);
		fireEvent.click(screen.getByRole("button"));
		auth.current = { ...auth.current, data: { satisfied: true } };
		rerender(<GitHubSetupPanel onReady={vi.fn()} />);
		expect(capture.mock.calls).toEqual([
			["ao.onboarding.step_viewed", expect.objectContaining({ step: "github_auth" })],
			["ao.onboarding.action_started", expect.objectContaining({ action: "gh_sign_in", target: "gh", step: "github_auth", attempt: 1 })],
			["ao.onboarding.action_finished", expect.objectContaining({ action: "gh_sign_in", target: "gh", outcome: "succeeded" })],
			["ao.onboarding.step_completed", expect.objectContaining({ step: "github_auth" })],
		]);
	});

	it("reports nothing from the clone dialog once onboarding is over", () => {
		auth.current = { ...auth.current, data: { satisfied: false } };
		const { rerender } = render(<GitHubSetupPanel onReady={vi.fn()} />);
		fireEvent.click(screen.getByRole("button"));
		auth.current = { ...auth.current, data: { satisfied: true } };
		rerender(<GitHubSetupPanel onReady={vi.fn()} />);
		expect(capture).not.toHaveBeenCalled();
	});

	it("leaves an onboarding attempt it didn't start alone when its own login fails", () => {
		// The Tools page's login, still watched, then the clone dialog after onboarding.
		setOnboardingTourStep("tour_project");
		startOnboardingAction("gh_sign_in", { target: "gh", step: "github_auth", auto: true });
		setOnboardingTourStep(undefined);
		auth.current = { ...auth.current, data: { satisfied: false } };
		const { rerender } = render(<GitHubSetupPanel onReady={vi.fn()} />);
		fireEvent.click(screen.getByRole("button"));
		login.isError = true;
		rerender(<GitHubSetupPanel onReady={vi.fn()} />);
		expect(sentryFailure).not.toHaveBeenCalled();
		expect(onboardingActionWatched("gh_sign_in", "gh")).toBe(true);
		expect(capture.mock.calls.map((call) => call[0])).not.toContain("ao.onboarding.action_finished");
	});

	it("reports its own login failing to start during onboarding", () => {
		setOnboardingTourStep("tour_project");
		auth.current = { ...auth.current, data: { satisfied: false } };
		const { rerender } = render(<GitHubSetupPanel onReady={vi.fn()} />);
		fireEvent.click(screen.getByRole("button"));
		login.isError = true;
		rerender(<GitHubSetupPanel onReady={vi.fn()} />);
		expect(sentryFailure.mock.calls).toEqual([["gh_sign_in", "start_failed", "gh"]]);
		expect(capture).toHaveBeenLastCalledWith("ao.onboarding.action_finished", expect.objectContaining({ action: "gh_sign_in", outcome: "failed", reason: "start_failed" }));
	});

	it("reports nothing when no GitHub setup was needed", () => {
		auth.current = { ...auth.current, data: { satisfied: true } };
		render(<GitHubSetupPanel onReady={vi.fn()} />);
		expect(capture).not.toHaveBeenCalled();
	});
});
