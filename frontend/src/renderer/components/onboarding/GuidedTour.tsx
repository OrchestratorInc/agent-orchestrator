import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { ArrowRight, X } from "lucide-react";
import { useEffect, useState, useSyncExternalStore } from "react";
import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";
import { useWorkspaceQuery, workspaceQueryKey } from "../../hooks/useWorkspaceQuery";
import { DEV_FAKE_PROJECT_ID, onDevFakeSend, setDevFakeProject } from "../../lib/dev-fake-project";
import { IS_DEV } from "../../lib/is-dev";
import { reportOnboardingStep } from "../../lib/onboarding-telemetry";
import { useUiStore } from "../../stores/ui-store";
import { CLOUD_PROJECT_KIND, STANDALONE_PROJECT_KIND } from "../../types/workspace";
import { Button } from "../ui/button";
import { Popover, PopoverAnchor, PopoverContent } from "../ui/popover";

// After setup, the real app walks the person through their first project and
// task: the control to use next is spotlit (the rest of the window dims) with
// a short card beside it, and each step moves on when the person does it, not
// on a Next button. Controls opt in with `data-tour="<step target>"`.
// Dev builds add Continue, and on the first step Continue with a fake project
// (dev-fake-project), so the whole tour can be previewed on a machine that
// already has projects; the card waits in a corner while its control is off
// screen.

type TourStep = "project" | "orchestrator" | "task";
const TOUR_STEPS: readonly TourStep[] = ["project", "orchestrator", "task"];
const TOUR_COPY = {
	project: { title: "firstRun.tourProjectTitle", body: "firstRun.tourProjectBody" },
	recent: { title: "firstRun.tourRecentTitle", body: "firstRun.tourRecentBody" },
	orchestrator: { title: "firstRun.tourOrchestratorTitle", body: "firstRun.tourOrchestratorBody" },
	task: { title: "firstRun.tourTaskTitle", body: "firstRun.tourTaskBody" },
} as const satisfies Record<TourStep | "recent", { title: string; body: string }>;
/** Each step's control, first match wins: Home's latest project (a machine
 * that has one opens it) or its add-project actions, the project board's
 * Orchestrator button (the empty board's, else the header's), then the
 * orchestrator chat's composer. */
const RECENT_PROJECT = '[data-tour="recent-project"]';
const TARGET: Record<TourStep, readonly string[]> = {
	project: [RECENT_PROJECT, '[data-tour="add-project"]'],
	orchestrator: ['[data-tour="orchestrator"]', '[data-tour="orchestrator-header"]'],
	task: ['[data-tour="composer"]'],
};
/** Lit together with a step's control when on screen: the chat's sample
 * prompts above its composer. */
const ALSO_LIT: Partial<Record<TourStep, string>> = { task: '[data-tour="composer-prompts"]' };
const findTarget = (step: TourStep) =>
	TARGET[step].reduce<Element | null>((found, selector) => found ?? document.querySelector(selector), null);
/** A step is done once the next one's control is on screen: a project board
 * (a project added or opened), then a chat's composer. */
const DONE_WHEN_SHOWN: Partial<Record<TourStep, TourStep>> = { project: "orchestrator", orchestrator: "task" };
/** Where the dev-build card waits while its control is off screen. */
const CORNER = { getBoundingClientRect: () => new DOMRect(window.innerWidth - 16, window.innerHeight - 16, 0, 0) };
// Any dialog over the app (Add Project's, the onboarding itself) hides the
// tour until it closes; the tour's own card doesn't count.
const OPEN_MODAL = '[role="dialog"]:not([data-guided-tour]), [role="alertdialog"]';
/** Room between the control and the spotlight's edge. */
const SPOTLIGHT_PAD = 6;

// The tour survives a relaunch mid-way (per-viewer convenience, so
// localStorage). `known` is the projects there when it started, so the first
// step waits for a new one; null until the project list has loaded. `fake`:
// a dev build is previewing with the pretend project.
type TourState = { step: TourStep; known: string[] | null; fake?: boolean };
const STORAGE_KEY = "ao.guidedTour";

function readState(): TourState | null {
	try {
		const parsed = JSON.parse(window.localStorage.getItem(STORAGE_KEY) ?? "null") as Partial<TourState> | null;
		if (!parsed || !TOUR_STEPS.includes(parsed.step as TourStep)) return null;
		return { step: parsed.step as TourStep, known: Array.isArray(parsed.known) ? parsed.known : null, fake: parsed.fake === true };
	} catch {
		return null;
	}
}

let state: TourState | null = typeof window === "undefined" ? null : readState();
setDevFakeProject(Boolean(state?.fake));
const listeners = new Set<() => void>();
function setState(next: TourState | null) {
	state = next;
	setDevFakeProject(Boolean(next?.fake));
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

/** Starts the tour from its first step (onboarding calls this as it closes). */
export function startGuidedTour() {
	setState({ step: "project", known: null });
}

type Rect = { top: number; left: number; width: number; height: number };
/** The element's box, grown to take in what's lit with it (when shown). */
function unionBox(element: Element, also: string | undefined): Rect {
	const box = element.getBoundingClientRect();
	const extra = also ? document.querySelector(also)?.getBoundingClientRect() : undefined;
	if (!extra || extra.width === 0 || extra.height === 0) return box;
	const top = Math.min(box.top, extra.top);
	const left = Math.min(box.left, extra.left);
	return { top, left, width: Math.max(box.right, extra.right) - left, height: Math.max(box.bottom, extra.bottom) - top };
}
const sameRect = (a: Rect | null, b: Rect | null) =>
	a === b || (!!a && !!b && a.top === b.top && a.left === b.left && a.width === b.width && a.height === b.height);

/** The step's control on screen; `sent` once the composer's chat has a message. */
type Target = { element: Element; rect: Rect; sent: boolean };

/** Follows the step's control every frame while the tour runs: "covered"
 * while a dialog is over the app, null while the control isn't on screen. */
function useTarget(step: TourStep | undefined) {
	const [target, setTarget] = useState<Target | "covered" | null>(null);
	useEffect(() => {
		if (!step) {
			setTarget(null);
			return;
		}
		let frame = 0;
		let last: Target | "covered" | null = null;
		const tick = () => {
			// Checked every frame: the next page's control can mount well after
			// this one's goes (a route still loading).
			const ahead = DONE_WHEN_SHOWN[step];
			if (ahead && state && findTarget(ahead)) {
				setState({ ...state, step: ahead });
				return;
			}
			if (document.querySelector(OPEN_MODAL)) {
				if (last !== "covered") setTarget((last = "covered"));
				frame = requestAnimationFrame(tick);
				return;
			}
			const element = findTarget(step);
			const box = element ? unionBox(element, ALSO_LIT[step]) : undefined;
			const next = element && box && box.width > 0 && box.height > 0
				? { element, rect: { top: box.top, left: box.left, width: box.width, height: box.height }, sent: element.hasAttribute("data-tour-sent") }
				: null;
			const previous = last === "covered" ? undefined : last;
			if (last === "covered" || next?.element !== previous?.element || next?.sent !== previous?.sent || !sameRect(next?.rect ?? null, previous?.rect ?? null)) {
				last = next;
				setTarget(next);
			}
			frame = requestAnimationFrame(tick);
		};
		tick();
		return () => cancelAnimationFrame(frame);
	}, [step]);
	return target;
}

export function GuidedTour() {
	const { t } = useTranslation();
	const tour = useSyncExternalStore(subscribe, () => state);
	const onboardingOpen = useUiStore((ui) => ui.firstRunOnboardingOpen);
	const step = tour && !onboardingOpen ? tour.step : undefined;
	const workspaces = useWorkspaceQuery().data;
	const localProjectIds = workspaces
		?.filter((project) => project.kind !== STANDALONE_PROJECT_KIND && project.kind !== CLOUD_PROJECT_KIND)
		.map((project) => project.id);
	const tracked = useTarget(step);
	const target = tracked === "covered" ? null : tracked;
	const navigate = useNavigate();
	const queryClient = useQueryClient();

	// The tour moves on: a new project (or, in useTarget, the next step's
	// control on screen), then the chat's first message.
	const projectsLoaded = localProjectIds !== undefined;
	const hasNewProject = Boolean(tour?.known && localProjectIds?.some((id) => !tour.known!.includes(id)));
	useEffect(() => {
		if (!tour || !projectsLoaded) return;
		if (tour.known === null) setState({ ...tour, known: localProjectIds ?? [] });
		else if (tour.step === "project" && hasNewProject) setState({ ...tour, step: "orchestrator" });
	}, [tour, projectsLoaded, hasNewProject]);
	// The pretend project's send stands in for the first message.
	useEffect(() => {
		if (!tour?.fake) return;
		return onDevFakeSend(() => {
			setState(null);
			void navigate({ to: "/" }).then(() => queryClient.refetchQueries({ queryKey: workspaceQueryKey }));
		});
	}, [tour?.fake, navigate, queryClient]);
	useEffect(() => {
		if (step !== "task") return;
		reportOnboardingStep("first_task", "viewed");
		if (target?.sent && !tour?.fake) {
			reportOnboardingStep("first_task", "completed");
			setState(null);
		}
	}, [step, target, tour?.fake]);

	if (!step || tracked === "covered" || (!target && !IS_DEV)) return null;
	const refetchProjects = () => queryClient.refetchQueries({ queryKey: workspaceQueryKey });
	const end = () => {
		if (step === "task" && !tour?.fake) reportOnboardingStep("first_task", "abandoned");
		setState(null);
		// The pretend project goes with the tour, so leave its pages first.
		if (tour?.fake) void navigate({ to: "/" }).then(refetchProjects);
	};
	// Dev builds: the first step adds the pretend project and opens its board
	// (which moves the tour on); its board's step opens a new chat in it, where
	// the composer is. Otherwise straight to the next step.
	const advance = async () => {
		if (step === "project") {
			setState({ ...tour!, fake: true });
			await refetchProjects();
			void navigate({ to: "/projects/$projectId", params: { projectId: DEV_FAKE_PROJECT_ID } });
			return;
		}
		if (step === "orchestrator" && tour?.fake) {
			void navigate({ to: "/new", search: { project: DEV_FAKE_PROJECT_ID } });
			return;
		}
		if (step === "task") end();
		else setState({ ...tour!, step: TOUR_STEPS[TOUR_STEPS.indexOf(step) + 1]! });
	};
	const rect = target?.rect;
	const copy = TOUR_COPY[step === "project" && target?.element.matches(RECENT_PROJECT) ? "recent" : step];
	return (
		<>
			{rect ? createPortal(
				// The spotlight: a box round the control whose huge spread dims the
				// rest of the window. It never takes the pointer, so the control
				// (and everything else) stays usable through it.
				<div
					aria-hidden="true"
					className="pointer-events-none fixed z-overlay rounded-xl transition-[top,left,width,height] duration-[280ms] ease-in-out motion-reduce:transition-none"
					style={{
						top: rect.top - SPOTLIGHT_PAD,
						left: rect.left - SPOTLIGHT_PAD,
						width: rect.width + SPOTLIGHT_PAD * 2,
						height: rect.height + SPOTLIGHT_PAD * 2,
						boxShadow: "0 0 0 200vmax var(--color-scrim)",
					}}
				/>,
				document.body,
			) : null}
			<Popover open>
				<PopoverAnchor virtualRef={{ current: target?.element ?? CORNER }} />
				<PopoverContent
					data-guided-tour=""
					aria-labelledby="guided-tour-title"
					side={target ? "bottom" : "top"}
					align={target ? "start" : "end"}
					sideOffset={SPOTLIGHT_PAD + 10}
					collisionPadding={16}
					updatePositionStrategy="always"
					// A guide, not a menu: it stays up while the person works and
					// leaves focus where it is.
					onOpenAutoFocus={(event) => event.preventDefault()}
					onInteractOutside={(event) => event.preventDefault()}
					onEscapeKeyDown={end}
					className="w-80 p-4"
				>
					<div className="flex items-center justify-between gap-3">
						<p className="text-xs text-muted-foreground">
							{t("firstRun.tourStepOf", { current: TOUR_STEPS.indexOf(step) + 1, total: TOUR_STEPS.length })}
						</p>
						<Button
							type="button"
							variant="ghost"
							size="icon-sm"
							className="-mr-1.5 -mt-1 text-muted-foreground"
							aria-label={t("firstRun.tourEnd")}
							title={t("firstRun.tourEnd")}
							onClick={end}
						>
							<X aria-hidden="true" />
						</Button>
					</div>
					<h2 id="guided-tour-title" className="pt-1 text-sm font-semibold text-foreground">{t(copy.title)}</h2>
					<p className="pt-1 text-pretty text-sm leading-relaxed text-muted-foreground">{t(copy.body)}</p>
					{IS_DEV ? (
						<div className="flex justify-end pt-3">
							<Button type="button" variant="secondary" size="sm" onClick={() => void advance()}>
								{t(step === "project" ? "firstRun.tourDevFake" : "firstRun.continue")}
								<ArrowRight aria-hidden="true" />
							</Button>
						</div>
					) : null}
				</PopoverContent>
			</Popover>
		</>
	);
}
