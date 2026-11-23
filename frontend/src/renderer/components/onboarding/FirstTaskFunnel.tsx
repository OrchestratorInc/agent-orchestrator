import { useRouterState } from "@tanstack/react-router";
import { useEffect, useSyncExternalStore } from "react";
import { useWorkspaceQuery } from "../../hooks/useWorkspaceQuery";
import { onComposerSent } from "../../lib/guided-tour-signals";
import type { OnboardingStep } from "../../lib/onboarding-steps";
import {
	endOnboardingReplay,
	ensureOnboardingStepViewed,
	leaveOnboardingActions,
	onboardingProjectSource,
	reportOnboardingStep,
	setOnboardingTourStep,
	useOnboardingActivation,
} from "../../lib/onboarding-telemetry";
import { useUiStore } from "../../stores/ui-store";
import { CLOUD_PROJECT_KIND, STANDALONE_PROJECT_KIND, STANDALONE_WORKSPACE_ID } from "../../types/workspace";

// After setup the app is on its own: Home with Add Project open, then the new
// project's orchestrator chat. There is no tour to walk the person through it;
// the app has to make the next step obvious. This only measures whether it
// does, as the onboarding funnel's last steps (their `tour_*` names predate
// the tour's removal and are kept so the funnel reads the same):
//
//   tour_project       a project is added, or an existing one opened
//   tour_orchestrator  a chat is showing (Add Project lands on the orchestrator's)
//   first_task         a message is sent from any chat
//
// It renders nothing.

type FunnelStep = "project" | "orchestrator" | "task";
const FUNNEL_STEP: Record<FunnelStep, OnboardingStep> = {
	project: "tour_project",
	orchestrator: "tour_orchestrator",
	task: "first_task",
};
const STEPS: readonly FunnelStep[] = ["project", "orchestrator", "task"];

/** A project's board or one of its chats (this machine's routes). */
const PROJECT_ROUTE = /^\/projects\/([^/]+)/;
/** A page with a chat composer: any session, or the new chat page. */
const CHAT_ROUTE = /(^|\/)sessions\/[^/]+|^\/new(\/|$)/;

// ── State: one run per install, kept across relaunches ──────────────────────

// `known`: the projects there when the funnel started, so a new one is told
// apart (null until the list loads). `projectId`: the project it settled on.
// `replay`: started by a Developer Mode replay, which lasts one launch.
// A per-viewer convenience, so localStorage (the key predates the rename).
type FunnelState = { step: FunnelStep; known: string[] | null; projectId?: string; replay?: boolean };
const STORAGE_KEY = "ao.guidedTour";

function readState(): FunnelState | null {
	try {
		const parsed = JSON.parse(window.localStorage.getItem(STORAGE_KEY) ?? "null") as Partial<FunnelState> | null;
		// A tour stored by an earlier version that had reached its closing note is over.
		if (!parsed || !STEPS.includes(parsed.step as FunnelStep) || parsed.replay) return null;
		return {
			step: parsed.step as FunnelStep,
			known: Array.isArray(parsed.known) ? parsed.known.filter((id): id is string => typeof id === "string") : null,
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
	ensureOnboardingStepViewed(FUNNEL_STEP[from]);
	reportOnboardingStep(FUNNEL_STEP[from], "completed", extra);
	const next = STEPS[STEPS.indexOf(from) + 1];
	if (next) {
		setState({ ...state, ...patch, step: next });
		return;
	}
	// The first task was sent: onboarding is over. An install or sign-in from
	// the onboarding pages still running ends here.
	leaveOnboardingActions();
	setState(null);
	endOnboardingReplay();
}

/** Starts measuring from the first step. Onboarding calls this as it closes
 * onto Home with Add Project open. */
export function startFirstTaskFunnel({ replay = false }: { replay?: boolean } = {}) {
	setState({ step: "project", known: null, ...(replay ? { replay } : {}) });
}

export function FirstTaskFunnel() {
	const run = useSyncExternalStore(subscribe, () => state);
	const onboardingOpen = useUiStore((ui) => ui.firstRunOnboardingOpen);
	const step = run && !onboardingOpen ? run.step : undefined;
	const workspaces = useWorkspaceQuery().data;
	const pathname = useRouterState({ select: (routerState) => routerState.location.pathname });

	useEffect(() => {
		setOnboardingTourStep(step ? FUNNEL_STEP[step] : undefined);
		if (step) reportOnboardingStep(FUNNEL_STEP[step], "viewed");
	}, [step]);
	// The first message after onboarding (activation). It runs before the
	// first-task listener below, so a message sent on that step counts as sent
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

	// The last step ends when any chat sends a message.
	useEffect(() => {
		if (step !== "task") return;
		return onComposerSent(() => moveOn("task"));
	}, [step]);

	return null;
}
