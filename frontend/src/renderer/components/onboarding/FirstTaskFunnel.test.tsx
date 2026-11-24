import { act, render } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const capture = vi.hoisted(() => vi.fn(async (_event: string, _props?: unknown) => undefined));
vi.mock("../../lib/telemetry", () => ({ captureRendererEvent: capture, captureRendererEventOnUnload: vi.fn(() => true) }));
vi.mock("../../lib/sentry", () => ({ captureOnboardingFailureToSentry: vi.fn() }));
const route = vi.hoisted(() => ({ pathname: "/" }));
vi.mock("@tanstack/react-router", () => ({
	useRouterState: ({ select }: { select: (state: { location: { pathname: string } }) => string }) => select({ location: { pathname: route.pathname } }),
}));
const workspaces = vi.hoisted(() => ({ data: undefined as Array<{ id: string; kind: string; sessions: never[] }> | undefined }));
vi.mock("../../hooks/useWorkspaceQuery", () => ({ useWorkspaceQuery: () => ({ data: workspaces.data }) }));

import { setAnalyticsOptedOut } from "../../lib/analytics-opt-out";
import { notifyComposerSent } from "../../lib/guided-tour-signals";
import { onboardingInProgress } from "../../lib/onboarding-flow";
import { beginOnboardingRun, completeOnboardingRun, reportOnboardingProjectAdded, resetOnboardingTelemetryForTests } from "../../lib/onboarding-telemetry";
import { useUiStore } from "../../stores/ui-store";
import { FirstTaskFunnel, landAfterSetup, noteAddProjectClosed, startFirstTaskFunnel } from "./FirstTaskFunnel";

/** The funnel events sent, as [event, step]. */
const funnel = () => capture.mock.calls
	.filter(([event]) => event.startsWith("ao.onboarding.step_"))
	.map(([event, props]) => [event, (props as { step: string }).step]);
const project = (id: string) => ({ id, kind: "single_repo", sessions: [] as never[] });

describe("FirstTaskFunnel", () => {
	beforeEach(() => {
		capture.mockClear();
		setAnalyticsOptedOut(false);
		window.localStorage.clear();
		resetOnboardingTelemetryForTests();
		useUiStore.setState({ firstRunOnboardingOpen: false });
		route.pathname = "/";
		workspaces.data = [];
		// Onboarding's pages finished, so the first message counts as activation.
		beginOnboardingRun("first_run", "welcome");
		completeOnboardingRun("finished");
		capture.mockClear();
	});

	it("measures the first project, its orchestrator chat and the first task without showing anything", () => {
		act(() => startFirstTaskFunnel());
		const view = render(<FirstTaskFunnel />);
		expect(view.container).toBeEmptyDOMElement();
		expect(funnel()).toEqual([["ao.onboarding.step_viewed", "tour_project"]]);
		expect(onboardingInProgress()).toBe(true);

		// Add Project clones a repository, then lands on its board.
		reportOnboardingProjectAdded("clone", "app");
		workspaces.data = [project("app")];
		route.pathname = "/projects/app";
		view.rerender(<FirstTaskFunnel />);
		expect(funnel()).toEqual([
			["ao.onboarding.step_viewed", "tour_project"],
			["ao.onboarding.step_completed", "tour_project"],
			["ao.onboarding.step_viewed", "tour_orchestrator"],
		]);
		expect(capture.mock.calls.find(([event]) => event === "ao.onboarding.step_completed")?.[1]).toMatchObject({ project_source: "clone" });

		// The new project's orchestrator chat opens on its own.
		route.pathname = "/projects/app/sessions/app-orchestrator";
		view.rerender(<FirstTaskFunnel />);
		expect(funnel().slice(3)).toEqual([
			["ao.onboarding.step_completed", "tour_orchestrator"],
			["ao.onboarding.step_viewed", "first_task"],
		]);

		capture.mockClear();
		act(() => notifyComposerSent());
		expect(capture.mock.calls.map(([event]) => event)).toEqual(["ao.onboarding.activated", "ao.onboarding.step_completed"]);
		expect(capture.mock.calls[0]?.[1]).toMatchObject({ during_tour: true, funnel_variant: "unguided" });
		expect(capture.mock.calls[1]?.[1]).toMatchObject({ step: "first_task", funnel_variant: "unguided" });
		expect(window.localStorage.getItem("ao.guidedTour")).toBeNull();
		expect(onboardingInProgress()).toBe(false);
	});

	it("counts a chat on a remote host as reaching a chat", () => {
		workspaces.data = [project("old")];
		act(() => startFirstTaskFunnel());
		const view = render(<FirstTaskFunnel />);
		route.pathname = "/projects/old";
		view.rerender(<FirstTaskFunnel />);
		route.pathname = "/host/devbox/session/remote-1";
		view.rerender(<FirstTaskFunnel />);
		expect(funnel().slice(-2)).toEqual([
			["ao.onboarding.step_completed", "tour_orchestrator"],
			["ao.onboarding.step_viewed", "first_task"],
		]);
	});

	it("counts opening a project that was already there", () => {
		workspaces.data = [project("old")];
		act(() => startFirstTaskFunnel());
		const view = render(<FirstTaskFunnel />);
		route.pathname = "/projects/old";
		view.rerender(<FirstTaskFunnel />);
		expect(capture.mock.calls.find(([event]) => event === "ao.onboarding.step_completed")?.[1]).toMatchObject({ step: "tour_project", project_source: "existing" });
	});

	it("waits while the onboarding window is still open", () => {
		useUiStore.setState({ firstRunOnboardingOpen: true });
		act(() => startFirstTaskFunnel());
		render(<FirstTaskFunnel />);
		expect(funnel()).toEqual([]);
	});

	it("ends on a message sent before the last step, skipping the steps it didn't reach", () => {
		act(() => startFirstTaskFunnel());
		render(<FirstTaskFunnel />);
		capture.mockClear();
		// Add Project closed; a chat started from New chat sends a message instead.
		act(() => notifyComposerSent());
		expect(capture.mock.calls.map(([event, props]) => [event, (props as { step?: string; reason?: string }).step, (props as { reason?: string }).reason])).toEqual([
			["ao.onboarding.activated", undefined, undefined],
			["ao.onboarding.step_skipped", "tour_project", "activated"],
			["ao.onboarding.step_skipped", "tour_orchestrator", "activated"],
			["ao.onboarding.step_skipped", "first_task", "activated"],
		]);
		expect(capture.mock.calls[0]?.[1]).toMatchObject({ during_tour: false });
		expect(window.localStorage.getItem("ao.guidedTour")).toBeNull();
		expect(onboardingInProgress()).toBe(false);
	});

	it("expires once the activation window has passed", () => {
		vi.useFakeTimers();
		try {
			act(() => startFirstTaskFunnel());
			const view = render(<FirstTaskFunnel />);
			view.unmount();
			vi.advanceTimersByTime(8 * 24 * 60 * 60_000);
			capture.mockClear();
			render(<FirstTaskFunnel />);
			expect(funnel().filter(([event]) => event === "ao.onboarding.step_skipped")).toEqual([
				["ao.onboarding.step_skipped", "tour_project"],
				["ao.onboarding.step_skipped", "tour_orchestrator"],
				["ao.onboarding.step_skipped", "first_task"],
			]);
			expect(capture.mock.calls.find(([event]) => event === "ao.onboarding.step_skipped")?.[1]).toMatchObject({ reason: "expired" });
			expect(onboardingInProgress()).toBe(false);
		} finally {
			vi.useRealTimers();
		}
	});

	it("counts opening a remote host's project board", () => {
		act(() => startFirstTaskFunnel());
		const view = render(<FirstTaskFunnel />);
		route.pathname = "/host/devbox/project/api";
		view.rerender(<FirstTaskFunnel />);
		expect(funnel()).toContainEqual(["ao.onboarding.step_completed", "tour_project"]);
	});

	it("records Add Project closed with no project, once, only when onboarding opened it", () => {
		act(() => startFirstTaskFunnel());
		render(<FirstTaskFunnel />);
		noteAddProjectClosed();
		expect(funnel().filter(([event]) => event === "ao.onboarding.step_skipped")).toEqual([]);
		act(() => startFirstTaskFunnel({ addProjectOpen: true }));
		noteAddProjectClosed();
		noteAddProjectClosed();
		const skips = capture.mock.calls.filter(([event]) => event === "ao.onboarding.step_skipped");
		expect(skips).toHaveLength(1);
		expect(skips[0]?.[1]).toMatchObject({ step: "tour_project", reason: "dismissed", funnel_variant: "unguided" });
	});

	it("picks up where it was on a later launch", async () => {
		act(() => startFirstTaskFunnel());
		const first = render(<FirstTaskFunnel />);
		route.pathname = "/projects/old";
		first.rerender(<FirstTaskFunnel />);
		first.unmount();
		expect(JSON.parse(window.localStorage.getItem("ao.guidedTour") ?? "null")).toMatchObject({ step: "orchestrator" });
		// A relaunch loads the module afresh from what was stored.
		vi.resetModules();
		const relaunched = await import("./FirstTaskFunnel");
		route.pathname = "/";
		capture.mockClear();
		const second = render(<relaunched.FirstTaskFunnel />);
		// Its view was already sent (once per install); reaching a chat completes it.
		route.pathname = "/projects/old/sessions/old-orchestrator";
		second.rerender(<relaunched.FirstTaskFunnel />);
		expect(funnel()).toEqual([
			["ao.onboarding.step_completed", "tour_orchestrator"],
			["ao.onboarding.step_viewed", "first_task"],
		]);
	});

	it("drops a tour stored by an earlier version that had finished", async () => {
		window.localStorage.setItem("ao.guidedTour", JSON.stringify({ step: "done", known: [] }));
		vi.resetModules();
		await import("./FirstTaskFunnel");
		expect(window.localStorage.getItem("ao.guidedTour")).toBeNull();
	});
});

describe("landAfterSetup", () => {
	beforeEach(() => {
		window.localStorage.clear();
		useUiStore.setState({ createProjectNonce: 0 });
	});

	it("opens Add Project on Home and starts the funnel", async () => {
		const navigateHome = vi.fn(async () => undefined);
		await landAfterSetup(navigateHome, { skipped: false, replay: false });
		expect(navigateHome).toHaveBeenCalledOnce();
		expect(useUiStore.getState().createProjectNonce).toBe(1);
		expect(JSON.parse(window.localStorage.getItem("ao.guidedTour") ?? "null")).toMatchObject({ step: "project", addProjectOpen: true });
	});

	it("only goes Home after Skip setup", async () => {
		const navigateHome = vi.fn(async () => undefined);
		await landAfterSetup(navigateHome, { skipped: true, replay: false });
		expect(navigateHome).toHaveBeenCalledOnce();
		expect(useUiStore.getState().createProjectNonce).toBe(0);
		expect(window.localStorage.getItem("ao.guidedTour")).toBeNull();
	});
});
