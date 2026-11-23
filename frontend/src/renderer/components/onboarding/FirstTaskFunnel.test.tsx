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
import { FirstTaskFunnel, startFirstTaskFunnel } from "./FirstTaskFunnel";

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
		expect(capture.mock.calls[0]?.[1]).toMatchObject({ during_tour: true });
		expect(window.localStorage.getItem("ao.guidedTour")).toBeNull();
		expect(onboardingInProgress()).toBe(false);
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
});
