import { useRouterState } from "@tanstack/react-router";
import { useEffect, useSyncExternalStore } from "react";
import { useWorkspaceQuery } from "../../hooks/useWorkspaceQuery";
import { onComposerSent } from "../../lib/guided-tour-signals";
import type { OnboardingStep } from "../../lib/onboarding-steps";
import {
	ONBOARDING_ACTIVATION_WINDOW_MS,
	endOnboardingReplay,
	ensureOnboardingStepViewed,
	leaveOnboardingActions,
	onboardingProjectSource,
	reportOnboardingStep,
	setOnboardingTourStep,
	skipOnboardingStep,
	useOnboardingActivation,
} from "../../lib/onboarding-telemetry";
import { useUiStore } from "../../stores/ui-store";
import { CLOUD_PROJECT_KIND, STANDALONE_PROJECT_KIND, STANDALONE_WORKSPACE_ID } from "../../types/workspace";

// After setup the app is on its own: Home with Add Project open, then the new
// project's orchestrator chat. There is no tour to walk the person through it;
// the app has to make the next step obvious. This only measures whether it
// does, as the onboarding funnel's last steps. Their `tour_*` names come from
// the guided tour that used to cover them; every event sent here carries
// `funnel_variant: "unguided"`, because the steps now complete on different
// signals (docs/telemetry.md):
//
//   tour_project       a project is added, or an existing one (local or remote) opened
//   tour_orchestrator  a chat is showing (Add Project lands on the orchestrator's)
//   first_task         a message is sent from a chat on that step
//
// The run ends on the first message whatever step it is on (the steps not
// reached are skipped as `activated`), or once the activation window has
// passed (`expired`). It renders nothing.

type FunnelStep = "project" | "orchestrator" | "task";
const FUNNEL_STEP: Record<FunnelStep, OnboardingStep> = {
	project: "tour_project",
	orchestrator: "tour_orchestrator",
	task: "first_task",
};
const STEPS: readonly FunnelStep[] = ["project", "orchestrator", "task"];
/** On every event this funnel sends, so it reads apart from the guided tour's. */
const VARIANT = { funnel_variant: "unguided" } as const;

/** A project's board or one of its chats, on this machine or a remote host. */
const PROJECT_ROUTE = /^\/(?:projects|host\/[^/]+\/project)\/([^/]+)/;
/** A page with a chat composer: any session on this machine
 * (`/sessions/…`), on a remote host (`/host/…/session/…`), or the new chat page. */
const CHAT_ROUTE = /(^|\/)sessions?\/[^/]+|^\/new(\/|$)/;

// ── State: one run per install, kept across relaunches ──────────────────────

// `known`: the projects there when the funnel started, so a new one is told
// apart (null until the list loads). `projectId`: the project it settled on.
// `startedAt`: when setup finished, for the activation window. `replay`:
// started by a Developer Mode replay, which lasts one launch. `addProjectOpen`:
// Add Project was opened for the person and they haven't closed it yet.
// A per-viewer convenience, so localStorage (the key predates the rename).
type FunnelState = {
	step: FunnelStep;
	known: string[] | null;
	startedAt: number;
	projectId?: string;
	replay?: boolean;
	addProjectOpen?: boolean;
};
const STORAGE_KEY = "ao.guidedTour";

function clearStored() {
	try {
		window.localStorage.removeItem(STORAGE_KEY);
	} catch {
		// Nothing to clear.
	}
}

function readState(): FunnelState | null {
	try {
		const parsed = JSON.parse(window.localStorage.getItem(STORAGE_KEY) ?? "null") as Partial<FunnelState> | null;
		if (!parsed) return null;
		// A tour stored by an earlier version that had reached its closing note,
		// or a replay from an earlier launch, is over.
		if (!STEPS.includes(parsed.step as FunnelStep) || parsed.replay) {
			clearStored();
			return null;
		}
		return {
			step: parsed.step as FunnelStep,
			known: Array.isArray(parsed.known) ? parsed.known.filter((id): id is string => typeof id === "string") : null,
			// A run stored before the window was tracked starts it now.
			startedAt: typeof parsed.startedAt === "number" ? parsed.startedAt : Date.now(),
			projectId: typeof parsed.projectId === "string" ? parsed.projectId : undefined,
		};
	} catch {
		return null;
	}
}

let state: FunnelState | null = typeof window === "undefined" ? null : readState();
const listeners = new Set<() => void>();
function setState(next: FunnelState | null) {
	state = next;
	try {
		if (next) window.localStorage.setItem(STORAGE_KEY, JSON.stringify(next));
		else window.localStorage.removeItem(STORAGE_KEY);
	} catch {
		// Kept in memory for this launch.
	}
	for (const listener of listeners) listener();
}
function subscribe(listener: () => void) {
	listeners.add(listener);
	return () => listeners.delete(listener);
}

/** Onboarding is over: anything still running from its pages ends here. */
function finishRun() {
	leaveOnboardingActions();
	setState(null);
	endOnboardingReplay();
}

/** Completes `from` if the funnel is still on it (a late signal changes nothing)
 * and moves on, or ends the run after the first task. */
function moveOn(from: FunnelStep, patch: Partial<FunnelState> = {}) {
	if (state?.step !== from) return;
	// The project step records where its project came from (a clone, a local
	// folder, one that was already there).
	const projectId = patch.projectId;
	const extra = from === "project" ? { project_source: onboardingProjectSource(projectId, !projectId || !(state.known ?? []).includes(projectId)) } : {};
	// A step can end before its view effect runs (restored on a page already
	// past it), so its view is reported first.
	ensureOnboardingStepViewed(FUNNEL_STEP[from], VARIANT);
	reportOnboardingStep(FUNNEL_STEP[from], "completed", { ...extra, ...VARIANT });
	const next = STEPS[STEPS.indexOf(from) + 1];
	if (next) {
		setState({ ...state, ...patch, step: next, addProjectOpen: false });
		return;
	}
	finishRun();
}

/** Ends the run early: the step it is on and those after it are skipped. */
function endEarly(reason: "activated" | "expired") {
	if (!state) return;
	for (const step of STEPS.slice(STEPS.indexOf(state.step))) skipOnboardingStep(FUNNEL_STEP[step], reason, VARIANT);
	finishRun();
}

/** Whether the activation window since setup finished has passed. */
function expired(run: FunnelState) {
	return Date.now() - run.startedAt > ONBOARDING_ACTIVATION_WINDOW_MS;
}

/** Starts measuring from the first step. */
export function startFirstTaskFunnel({ replay = false, addProjectOpen = false }: { replay?: boolean; addProjectOpen?: boolean } = {}) {
	setState({ step: "project", known: null, startedAt: Date.now(), ...(replay ? { replay } : {}), ...(addProjectOpen ? { addProjectOpen } : {}) });
}

/**
 * Where onboarding lands once its pages are done: Home, with Add Project open
 * and the funnel started. A skipped setup has no harness to run a project's
 * agents with, so it lands on Home without either.
 */
export async function landAfterSetup(navigateHome: () => Promise<unknown>, { skipped, replay }: { skipped: boolean; replay: boolean }) {
	await navigateHome();
	if (skipped) return;
	startFirstTaskFunnel({ replay, addProjectOpen: true });
	useUiStore.getState().requestCreateProject();
}

/** Add Project was closed. When it was the one opened for the person and no
 * project came of it, the project step records that it was dismissed. */
export function noteAddProjectClosed() {
	if (state?.step !== "project" || !state.addProjectOpen) return;
	skipOnboardingStep("tour_project", "dismissed", VARIANT);
	setState({ ...state, addProjectOpen: false });
}

export function FirstTaskFunnel() {
	const run = useSyncExternalStore(subscribe, () => state);
	const onboardingOpen = useUiStore((ui) => ui.firstRunOnboardingOpen);
	const step = run && !onboardingOpen ? run.step : undefined;
	const workspaces = useWorkspaceQuery().data;
	const pathname = useRouterState({ select: (routerState) => routerState.location.pathname });

	useEffect(() => {
		setOnboardingTourStep(step ? FUNNEL_STEP[step] : undefined);
		if (step) reportOnboardingStep(FUNNEL_STEP[step], "viewed", VARIANT);
	}, [step]);
	// Past the activation window the run is over, even if the person never
	// came back to it: checked as the funnel shows (a relaunch, a new step) and
	// again when the window ends while the app stays open on one step.
	useEffect(() => {
		if (!step || !state) return;
		if (expired(state)) {
			endEarly("expired");
			return;
		}
		const timer = window.setTimeout(() => {
			if (state && expired(state)) endEarly("expired");
		}, state.startedAt + ONBOARDING_ACTIVATION_WINDOW_MS - Date.now() + 1);
		return () => window.clearTimeout(timer);
	}, [step]);
	// The first message after onboarding (activation). It runs before the
	// listener below, so a message sent on the first-task step counts as sent
	// while onboarding was still going.
	useOnboardingActivation(() => state?.step === "task");

	// A project added since the start ends the first step (Add Project can land
	// on its board a beat later).
	const localProjectIds = workspaces
		?.filter((project) => project.kind !== STANDALONE_PROJECT_KIND && project.kind !== CLOUD_PROJECT_KIND)
		.map((project) => project.id);
	const listKey = localProjectIds?.join("\n");
	useEffect(() => {
		if (!state || !localProjectIds) return;
		if (state.known === null) {
			setState({ ...state, known: localProjectIds });
			return;
		}
		const known = state.known;
		const added = localProjectIds.find((id) => !known.includes(id));
		if (added) moveOn("project", { projectId: added });
		// listKey stands for localProjectIds, which is a fresh array each render.
	}, [run, listKey]);

	// Opening a project that was already there also ends it; then any chat
	// showing ends the next (a new project lands on its orchestrator's chat).
	useEffect(() => {
		if (step === "project") {
			const projectId = PROJECT_ROUTE.exec(pathname)?.[1];
			if (projectId && projectId !== STANDALONE_WORKSPACE_ID) moveOn("project", { projectId: decodeURIComponent(projectId) });
		} else if (step === "orchestrator" && CHAT_ROUTE.test(pathname)) {
			moveOn("orchestrator");
		}
	}, [step, pathname]);

	// A message ends the run: the first task on its own step, or, sent before
	// the funnel got there (a chat started another way), early.
	useEffect(() => {
		if (!step) return;
		return onComposerSent(() => {
			// A message after the window (a timer that slept with the machine) isn't the first task.
			if (state && expired(state)) endEarly("expired");
			else if (state?.step === "task") moveOn("task");
			else endEarly("activated");
		});
	}, [step]);

	return null;
}
