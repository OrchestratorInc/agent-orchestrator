import { useNavigate, useRouter, useRouterState } from "@tanstack/react-router";
import { ArrowRight, X } from "@/components/app-icons";
import { useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore } from "react";
import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";
import { useWorkspaceQuery } from "../../hooks/useWorkspaceQuery";
import { onComposerSent } from "../../lib/guided-tour-signals";
import { IS_DEV } from "../../lib/is-dev";
import { sessionNavigateTarget } from "../../lib/navigate-to-session";
import { reportOnboardingStep } from "../../lib/onboarding-telemetry";
import { cn } from "../../lib/utils";
import { useUiStore } from "../../stores/ui-store";
import { CLOUD_PROJECT_KIND, findProjectOrchestrator, STANDALONE_PROJECT_KIND } from "../../types/workspace";
import { Button } from "../ui/button";

// After setup, the real app walks the person through their first project and
// task. The control to use next is lit (the rest of the window softly fades
// back) with a short card beside it, and each step moves on when the person
// does it: open or add a project, open the orchestrator, send it a message.
// When that control isn't on screen (they wandered off, or the page is still
// loading) the card waits in the corner with a way back. Controls opt in with
// `data-tour="…"`. Dev builds add Continue on the steps after the first, to
// step through the rest of the tour.
//
// The card is not the shared Popover: that one is a dialog, and pages treat
// an open dialog as owning Escape and focus. This is a guide beside the work.

type TourStep = "project" | "orchestrator" | "task" | "done";
/** The steps the person does, counted on the card. */
const COUNTED: readonly TourStep[] = ["project", "orchestrator", "task"];
const COPY = {
	project: { title: "firstRun.tourProjectTitle", body: "firstRun.tourProjectBody" },
	recent: { title: "firstRun.tourRecentTitle", body: "firstRun.tourRecentBody" },
	orchestrator: { title: "firstRun.tourOrchestratorTitle", body: "firstRun.tourOrchestratorBody" },
	task: { title: "firstRun.tourTaskTitle", body: "firstRun.tourTaskBody" },
	done: { title: "firstRun.tourDoneTitle", body: "firstRun.tourDoneBody" },
} as const satisfies Record<TourStep | "recent", { title: string; body: string }>;

/** Each step's control, first match wins: Home's projects section (its add
 * actions and, on a machine that has some, recent projects); the board's
 * Orchestrator button (the empty board's, else the page header's); a chat's
 * composer. */
const RECENT_PROJECT = '[data-tour="recent-project"]';
const TARGET: Record<Exclude<TourStep, "done">, readonly string[]> = {
	project: ['[data-tour="add-project"]'],
	orchestrator: ['[data-tour="orchestrator"]', '[data-tour="orchestrator-header"]'],
	task: ['[data-tour="composer"]'],
};
/** Lit with the composer when shown: its sample prompts and project tray. */
const ALSO_LIT: Partial<Record<TourStep, readonly string[]>> = {
	task: ['[data-tour="composer-prompts"]', '[data-tour="composer-tray"]'],
};
/** A step is done once the next one's control is on screen: any project
 * board (one added or opened), then any chat's composer. */
const DONE_WHEN_SHOWN: Partial<Record<TourStep, Exclude<TourStep, "done">>> = { project: "orchestrator", orchestrator: "task" };
const BOARD = '[data-testid="board"][data-project-id]';
// A dialog over the app (Add Project's, Settings, the palette, the onboarding
// itself) owns the moment: the tour steps aside until it closes.
const OPEN_DIALOG = '[role="dialog"], [role="alertdialog"]';

/** Room between the control and the lit area's edge. */
const LIT_PAD = 6;
/** Between the lit area and the card. */
const CARD_GAP = 12;
/** From the window's edges. */
const EDGE = 16;
/** How long a control may be gone before the card docks: a page swap takes a
 * frame or two, which shouldn't make the card jump. */
const MISSING_GRACE_MS = 400;
/** The lit area and card glide from one control to the next over this. */
const GLIDE_MS = 280;
/** The closing note leaves on its own after this. */
const DONE_HOLD_MS = 12_000;

/** On screen, not just in the page: chats stay mounted (hidden) after they're
 * left, and a hidden composer or dialog must not count. */
function isVisible(element: Element): boolean {
	const box = element.getBoundingClientRect();
	return box.width > 0 && box.height > 0 && element.checkVisibility({ checkVisibilityCSS: true });
}
/** The first visible match, trying the selectors in order. */
function findVisible(selectors: readonly string[]): Element | null {
	for (const selector of selectors) {
		for (const element of document.querySelectorAll(selector)) if (isVisible(element)) return element;
	}
	return null;
}

// ── State: one tour per install, kept across relaunches ──────────────────────

// `known`: the projects there when the tour started, so the first step can
// tell a new one (null until the list loads). `projectId`: the project the
// tour settled on (added or opened). A per-viewer convenience, so localStorage.
type TourState = { step: TourStep; known: string[] | null; projectId?: string };
const STORAGE_KEY = "ao.guidedTour";
const STEPS: readonly TourStep[] = [...COUNTED, "done"];

function readState(): TourState | null {
	try {
		const parsed = JSON.parse(window.localStorage.getItem(STORAGE_KEY) ?? "null") as Partial<TourState> | null;
		if (!parsed || !STEPS.includes(parsed.step as TourStep)) return null;
		return {
			step: parsed.step as TourStep,
			known: Array.isArray(parsed.known) ? parsed.known.filter((id): id is string => typeof id === "string") : null,
			projectId: typeof parsed.projectId === "string" ? parsed.projectId : undefined,
		};
	} catch {
		return null;
	}
}

let state: TourState | null = typeof window === "undefined" ? null : readState();
const listeners = new Set<() => void>();
function setState(next: TourState | null) {
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
/** Moves on from `from` only if the tour is still there (a late signal from a
 * step already passed changes nothing). */
function moveOn(from: TourStep, to: TourStep, patch: Partial<TourState> = {}) {
	if (state?.step === from) setState({ ...state, ...patch, step: to });
}

/** Starts the tour from its first step. Onboarding calls this once it has
 * closed and Home, where projects are added, is showing. */
export function startGuidedTour() {
	setState({ step: "project", known: null });
}

// ── Following the control on screen ─────────────────────────────────────────

type Rect = { top: number; left: number; width: number; height: number };
// "pending": a step's first moments, before its control has shown up (the
// card waits rather than docking); "missing": still not there after that.
type Seen =
	| { kind: "pending" }
	| { kind: "covered" }
	| { kind: "missing" }
	| { kind: "shown"; element: Element; rect: Rect; radius: number };

/** The box of the control and what's lit with it. */
function litBox(element: Element, also: readonly string[] = []): Rect {
	let { top, left, right, bottom } = element.getBoundingClientRect();
	for (const selector of also) {
		const extra = findVisible([selector])?.getBoundingClientRect();
		if (!extra) continue;
		top = Math.min(top, extra.top);
		left = Math.min(left, extra.left);
		right = Math.max(right, extra.right);
		bottom = Math.max(bottom, extra.bottom);
	}
	return { top, left, width: right - left, height: bottom - top };
}

/** The control's own corner radius (it or the first rounded part inside it),
 * so the lit area follows its shape. */
function cornerRadius(element: Element): number {
	const queue: Element[] = [element];
	for (let checked = 0; queue.length && checked < 24; checked++) {
		const node = queue.shift()!;
		const radius = Number.parseFloat(getComputedStyle(node).borderTopLeftRadius);
		if (radius > 0) return Math.min(radius, 28);
		queue.push(...Array.from(node.children));
	}
	return 8;
}

const sameRect = (a: Rect, b: Rect) =>
	Math.abs(a.top - b.top) < 0.5 && Math.abs(a.left - b.left) < 0.5 && Math.abs(a.width - b.width) < 0.5 && Math.abs(a.height - b.height) < 0.5;

/** Watches the page every frame while a step runs: moves the tour on once the
 * next step's control shows up (a page can mount well after the last one
 * went), and reports where this step's control is. */
function useSeen(step: TourStep | undefined): Seen {
	const [seen, setSeen] = useState<Seen>({ kind: "pending" });
	// A layout effect, so a step's first look happens before it paints: the
	// card never shows a frame at the last step's control or in the corner.
	useLayoutEffect(() => {
		if (!step || step === "done") {
			setSeen({ kind: "missing" });
			return;
		}
		let frame = 0;
		let last: Seen | undefined;
		let lastShownAt = performance.now();
		const report = (next: Seen) => {
			const unchanged = last !== undefined && next.kind === last.kind && (
				next.kind !== "shown" || (last.kind === "shown" && next.element === last.element && sameRect(next.rect, last.rect))
			);
			if (unchanged) return;
			last = next;
			setSeen(next);
		};
		const tick = () => {
			frame = requestAnimationFrame(tick);
			const ahead = DONE_WHEN_SHOWN[step];
			if (ahead && findVisible(TARGET[ahead])) {
				const board = findVisible([BOARD])?.getAttribute("data-project-id") ?? undefined;
				moveOn(step, ahead, board && step === "project" ? { projectId: board } : {});
				return;
			}
			if (findVisible([OPEN_DIALOG])) {
				report({ kind: "covered" });
				return;
			}
			const element = findVisible(TARGET[step]);
			const rect = element ? litBox(element, ALSO_LIT[step]) : undefined;
			if (element && rect && rect.width > 0 && rect.height > 0) {
				lastShownAt = performance.now();
				const radius = last?.kind === "shown" && last.element === element ? last.radius : cornerRadius(element);
				report({ kind: "shown", element, rect, radius });
				return;
			}
			// Gone for a moment (a page swap) keeps the last place; gone for
			// longer docks the card.
			if (performance.now() - lastShownAt > MISSING_GRACE_MS) report({ kind: "missing" });
			else if (last?.kind !== "shown") report({ kind: "pending" });
		};
		tick();
		return () => cancelAnimationFrame(frame);
	}, [step]);
	return seen;
}

function useWindowSize() {
	const [size, setSize] = useState(() => ({ width: window.innerWidth, height: window.innerHeight }));
	useEffect(() => {
		const onResize = () => setSize({ width: window.innerWidth, height: window.innerHeight });
		window.addEventListener("resize", onResize);
		return () => window.removeEventListener("resize", onResize);
	}, []);
	return size;
}

/** Under the lit area when it fits, else over it, centred on it and kept
 * inside the window; with nothing lit, the bottom-right corner. */
function placeCard(lit: Rect | undefined, card: { width: number; height: number }, view: { width: number; height: number }) {
	if (!lit) return { top: view.height - card.height - EDGE, left: view.width - card.width - EDGE };
	const below = lit.top + lit.height + LIT_PAD + CARD_GAP;
	const above = lit.top - LIT_PAD - CARD_GAP - card.height;
	const top = below + card.height <= view.height - EDGE ? below : above >= EDGE ? above : view.height - card.height - EDGE;
	const centred = lit.left + lit.width / 2 - card.width / 2;
	const left = Math.min(Math.max(centred, EDGE), view.width - card.width - EDGE);
	return { top, left };
}

// ── The tour ────────────────────────────────────────────────────────────────

export function GuidedTour() {
	const { t } = useTranslation();
	const tour = useSyncExternalStore(subscribe, () => state);
	const onboardingOpen = useUiStore((ui) => ui.firstRunOnboardingOpen);
	const step = tour && !onboardingOpen ? tour.step : undefined;
	const workspaces = useWorkspaceQuery().data;
	const navigate = useNavigate();
	const router = useRouter();
	const pathname = useRouterState({ select: (routerState) => routerState.location.pathname });
	const seen = useSeen(step);
	const view = useWindowSize();

	// The first step also ends when a project appears that wasn't there at the
	// start (Add Project can land on its board a beat later).
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
		if (added) moveOn("project", "orchestrator", { projectId: added });
		// listKey stands for localProjectIds, which is a fresh array each render.
	}, [tour, listKey]);

	// The last step ends when any chat sends a message.
	useEffect(() => {
		if (step !== "task") return;
		reportOnboardingStep("first_task", "viewed");
		return onComposerSent(() => {
			reportOnboardingStep("first_task", "completed");
			moveOn("task", "done");
		});
	}, [step]);

	const end = () => {
		if (state?.step === "task") reportOnboardingStep("first_task", "abandoned");
		setState(null);
	};
	const endRef = useRef(end);
	endRef.current = end;
	// The closing note doesn't need dismissing.
	useEffect(() => {
		if (step !== "done") return;
		const timer = window.setTimeout(() => endRef.current(), DONE_HOLD_MS);
		return () => window.clearTimeout(timer);
	}, [step]);

	// The card's own size, for placing it.
	const cardRef = useRef<HTMLDivElement>(null);
	const [cardSize, setCardSize] = useState({ width: 320, height: 160 });
	useLayoutEffect(() => {
		const card = cardRef.current;
		if (!card) return;
		const measure = () => setCardSize((size) =>
			size.width === card.offsetWidth && size.height === card.offsetHeight ? size : { width: card.offsetWidth, height: card.offsetHeight });
		measure();
		const observer = new ResizeObserver(measure);
		observer.observe(card);
		return () => observer.disconnect();
	});

	// Gliding only when the card or lit area moves to another control (or
	// docks); following a control as the page scrolls or resizes is instant.
	const shown = seen.kind === "shown" ? seen : undefined;
	const anchorKey = shown ? `${step}:${shown.element.getAttribute("data-tour") ?? ""}` : `${step}:corner`;
	const [gliding, setGliding] = useState(false);
	const lastAnchor = useRef(anchorKey);
	useEffect(() => {
		if (lastAnchor.current === anchorKey) return;
		lastAnchor.current = anchorKey;
		setGliding(true);
		const timer = window.setTimeout(() => setGliding(false), GLIDE_MS + 40);
		return () => window.clearTimeout(timer);
	}, [anchorKey]);
	// The lit area keeps its last place while it fades out.
	const lastLit = useRef<{ rect: Rect; radius: number } | null>(null);
	if (shown) lastLit.current = { rect: shown.rect, radius: shown.radius };

	if (!step || !tour || seen.kind === "covered") return null;

	// Where "Take me there" goes for a step whose control is off screen.
	const projectId = tour.projectId;
	const orchestrator = projectId && workspaces ? findProjectOrchestrator(workspaces, projectId) : undefined;
	const destination =
		step === "project" || !projectId ? { to: "/" as const }
		: step === "orchestrator" ? { to: "/projects/$projectId" as const, params: { projectId } }
		: step === "task" && orchestrator ? sessionNavigateTarget(projectId, orchestrator.id)
		: { to: "/projects/$projectId" as const, params: { projectId } };
	const destinationPath = router.buildLocation(destination).pathname;
	const offTrack = step !== "done" && !shown;
	const showTakeMeThere = offTrack && pathname !== destinationPath;

	// Dev builds: straight to the next step. The first step has none, since a
	// project is needed for the rest.
	const devContinue = () => {
		if (step === "done") end();
		else moveOn(step, STEPS[STEPS.indexOf(step) + 1]!);
	};
	const showDevContinue = IS_DEV && step !== "done" && step !== "project";

	const copy = COPY[step === "project" && shown?.element.querySelector(RECENT_PROJECT) ? "recent" : step];
	const position = placeCard(step === "done" ? undefined : shown?.rect, cardSize, view);
	const glide = gliding ? `top ${GLIDE_MS}ms ease-in-out, left ${GLIDE_MS}ms ease-in-out` : undefined;
	const lit = lastLit.current;
	const counted = COUNTED.indexOf(step);

	return createPortal(
		<>
			{/* The lit area: a box round the control whose huge spread fades the
			    rest of the window back. It never takes the pointer, so the
			    control (and everything else) stays usable through it. */}
			{lit ? (
				<div
					aria-hidden="true"
					className="pointer-events-none fixed z-overlay motion-reduce:transition-none"
					style={{
						top: lit.rect.top - LIT_PAD,
						left: lit.rect.left - LIT_PAD,
						width: lit.rect.width + LIT_PAD * 2,
						height: lit.rect.height + LIT_PAD * 2,
						borderRadius: lit.radius + LIT_PAD,
						opacity: shown && step !== "done" ? 1 : 0,
						boxShadow: "0 0 0 1.5px var(--tour-ring), 0 0 0 200vmax var(--tour-scrim)",
						transition: [
							"opacity 200ms ease-out",
							gliding ? `top ${GLIDE_MS}ms ease-in-out, left ${GLIDE_MS}ms ease-in-out, width ${GLIDE_MS}ms ease-in-out, height ${GLIDE_MS}ms ease-in-out, border-radius ${GLIDE_MS}ms ease-in-out` : "",
						].filter(Boolean).join(", "),
					}}
				/>
			) : null}
			{seen.kind === "pending" && step !== "done" ? null : <section
				ref={cardRef}
				data-guided-tour=""
				aria-labelledby="guided-tour-title"
				className="fixed z-overlay w-80 animate-popover-in rounded-xl border border-(--color-border-overlay) bg-overlay p-4 text-popover-foreground shadow-(--shadow-popover) motion-reduce:animate-none motion-reduce:transition-none"
				style={{ top: position.top, left: position.left, transition: glide }}
			>
				<Button
					type="button"
					variant="ghost"
					size="icon-sm"
					className="absolute right-2.5 top-2.5 text-muted-foreground"
					aria-label={t("firstRun.tourEnd")}
					title={t("firstRun.tourEnd")}
					onClick={end}
				>
					<X aria-hidden="true" />
				</Button>
				{counted >= 0 ? (
					<div className="flex h-5 items-center gap-2.5 pb-2">
						<div aria-hidden="true" className="flex items-center gap-1">
							{COUNTED.map((countedStep, index) => (
								<span
									key={countedStep}
									className={cn("h-1 w-5 rounded-full transition-colors duration-normal", index <= counted ? "bg-foreground" : "bg-muted")}
								/>
							))}
						</div>
						<span className="text-xs text-muted-foreground">
							{t("firstRun.tourStepOf", { current: counted + 1, total: COUNTED.length })}
						</span>
					</div>
				) : null}
				{/* Announced as it changes, so each step is read out as it arrives. */}
				<div aria-live="polite" className="pr-6">
					<h2 id="guided-tour-title" className="pt-1 text-sm font-semibold text-foreground">{t(copy.title)}</h2>
					<p className="pt-1 text-pretty text-sm leading-relaxed text-muted-foreground">{t(copy.body)}</p>
				</div>
				{showTakeMeThere || step === "done" || showDevContinue ? (
					<div className="flex flex-wrap items-center justify-end gap-2 pt-4">
						{showDevContinue ? (
							<Button type="button" variant="ghost" size="sm" className="text-muted-foreground" onClick={devContinue}>
								{t("firstRun.continue")}
							</Button>
						) : null}
						{showTakeMeThere ? (
							<Button type="button" variant="secondary" size="sm" onClick={() => void navigate(destination)}>
								{t("firstRun.tourTakeMeThere")}
								<ArrowRight aria-hidden="true" />
							</Button>
						) : null}
						{step === "done" ? (
							<Button type="button" variant="primary" size="sm" onClick={end}>
								{t("firstRun.tourDone")}
							</Button>
						) : null}
					</div>
				) : null}
			</section>}
		</>,
		document.body,
	);
}
