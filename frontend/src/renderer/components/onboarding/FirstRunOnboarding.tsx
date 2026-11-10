import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { ArrowLeft, ArrowRight, Cloud, LoaderCircle, RefreshCw, Smartphone, type LucideIcon } from "lucide-react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import {
	createContext,
	useCallback,
	useContext,
	useEffect,
	useId,
	useLayoutEffect,
	useMemo,
	useRef,
	useState,
	type ComponentProps,
	type ReactNode,
} from "react";
import { createPortal } from "react-dom";
import { Trans, useTranslation } from "react-i18next";
import type { MessageKey } from "../../i18n";
import { useAgentAuthPlans, useStartAgentAuth } from "../../hooks/useAgentAuth";
import {
	agentReadinessQueryKey,
	cacheAgentReadiness,
	ensureAgentReadiness,
	useAgentReadinessQuery,
} from "../../hooks/useAgentReadinessQuery";
import { useCloudGate } from "../../hooks/useCloudGate";
import { useCloudLocalAuth } from "../../hooks/useCloudLocalAuth";
import { useMigrationOffer } from "../../hooks/useMigrationOffer";
import {
	githubAuthRequirementQueryOptions,
	systemRequirementsQueryKey,
	useGitHubAuthRequirement,
	useSystemRequirementsGate,
} from "../../hooks/useSystemRequirementsGate";
import { useWorkspaceQuery } from "../../hooks/useWorkspaceQuery";
import type { AgentId } from "../../lib/agent-options";
import { DEFAULT_AGENT_PRIORITY_RANK } from "../../lib/agent-select-options";
import { freshMachineSimActive, setFreshMachineSim } from "../../lib/fresh-machine-sim";
import { aoBridge } from "../../lib/bridge";
import { useCloudSession } from "../../lib/cloud-session";
import { useOnboardingStep } from "../../lib/onboarding-telemetry";
import { usesPreviewWorkspaceData } from "../../lib/preview-mode";
import { useShell } from "../../lib/shell-context";
import { cn } from "../../lib/utils";
import { useLocalSignInDialogStore } from "../../stores/local-signin-dialog-store";
import { useUiStore } from "../../stores/ui-store";
import { STANDALONE_PROJECT_KIND } from "../../types/workspace";
import { AgentAvatar } from "../AgentAvatar";
import { CloudLocalSignInDialog } from "../CloudLocalSignInDialog";
import { GitHubIcon } from "../GitHubIcon";
import { GitHubOnboardingNotice } from "../GitHubOnboardingNotice";
import { dragStyle } from "../PageTopbar";
import { closeAuthTerminal, HarnessAuthTerminalPanel, type AuthTerminalWorkflow } from "../settings/harness/HarnessAuthTerminalPanel";
import { Button } from "../ui/button";
import { startGuidedTour } from "./GuidedTour";
import { agentRowState, InstallDocsLink, InstallProblem, InstallProgress, useAgentInstalls, useGhInstall, type AgentRowState } from "./ToolInstalls";
import { PreviewStage, type PreviewCard, type ScenePage } from "./OnboardingPreviews";
import { SchematicPhoneScreen, SchematicScene, type SceneFocus } from "./OnboardingSchematic";

// Three pages: what Orchestrator is (with its three-part story playing in the
// preview), the optional account, and the tools a worker needs (GitHub CLI and
// a coding agent). Log in and Your tools are skipped when there is nothing to
// do on them. The first project and task follow in the real app, as a guided
// tour (GuidedTour).
const STEPS = ["welcome", "signin", "tools"] as const;
type Step = (typeof STEPS)[number];
// Where a step stored by the earlier seven-page flow resumes.
const LEGACY_STEPS: Record<string, Step> = {
	github: "tools",
	agents: "tools",
	orchestrator: "tools",
	start: "tools",
	project: "tools",
};
// Resume on the same page after a relaunch mid-setup (per-viewer convenience).
const STEP_STORAGE_KEY = "ao.firstRun.step";
// Always listed so a machine with no agents still has something to install.
const SUGGESTED_AGENTS = ["claude-code", "codex"];
// Where to get the GitHub CLI when it can't be installed from here.
const GITHUB_CLI_URL = "https://cli.github.com/";
const READINESS_POLL_MS = 3_000;
// --duration-normal and --ease-out, for motion's JS-driven transitions.
const FADE = { duration: 0.15, ease: [0.23, 1, 0.32, 1] } as const;
/** The copy column's width beside the stage. */
const COPY_WIDTH = 520;

/** "How Orchestrator works": the three parts the welcome preview plays. */
const ORCH_POINTS = [
	{ title: "firstRun.orchPoint1Title", body: "firstRun.orchPoint1Body" },
	{ title: "firstRun.orchPoint2Title", body: "firstRun.orchPoint2Body" },
	{ title: "firstRun.orchPointCiTitle", body: "firstRun.orchPointCiBody" },
	{ title: "firstRun.orchPoint3Title", body: "firstRun.orchPoint3Body" },
] as const;
/** The part where pull requests go through CI and code review on GitHub. */
const CI_POINT = 2;
/** The welcome captions: the outgoing one fades this fast, then the next comes in. */
const CAPTION_OUT_S = 0.16;
const CAPTION_EASE = [0.23, 1, 0.32, 1] as const;
/** How long each part of the story holds before the next plays. */
const POINT_HOLD_MS = [4200, 3800, 8800, 4600] as const;
/** The order the example work settles (task indexes). The CI part settles the
 * first three, the ones with pull requests, a beat apart: long enough for each
 * round trip to GitHub (OnboardingSchematic). The last part settles the one
 * waiting on you. */
const SORT_ORDER = [3, 2, 0, 1] as const;
const PR_SORTS = 3;
const SORT_BEAT_MS = 2200;


function readStoredStep(): Step {
	try {
		const stored = window.localStorage.getItem(STEP_STORAGE_KEY);
		if (STEPS.includes(stored as Step)) return stored as Step;
		return (stored && LEGACY_STEPS[stored]) || "welcome";
	} catch {
		return "welcome";
	}
}

function storeStep(step: Step | null) {
	try {
		if (step) window.localStorage.setItem(STEP_STORAGE_KEY, step);
		else window.localStorage.removeItem(STEP_STORAGE_KEY);
	} catch {
		// Resuming is a convenience; the flow still works without storage.
	}
}

/** Full-window first-run onboarding. Shown once per fresh install (the
 * Electron app marker's first-run flag), over the shell, until the user
 * finishes or skips it. Existing installs and installs with projects never
 * see it. */
export function FirstRunOnboarding() {
	const { daemonStatus } = useShell();
	const workspaceQuery = useWorkspaceQuery();
	const migrationOffer = useMigrationOffer();
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const [status, setStatus] = useState<"loading" | "pending" | "seen">("loading");
	const [open, setOpen] = useState(false);
	const setOnboardingOpen = useUiStore((state) => state.setFirstRunOnboardingOpen);
	useEffect(() => {
		setOnboardingOpen(open);
		return () => setOnboardingOpen(false);
	}, [open, setOnboardingOpen]);
	const projectCount = (workspaceQuery.data ?? []).filter((project) => project.kind !== STANDALONE_PROJECT_KIND).length;
	const ready =
		(usesPreviewWorkspaceData || daemonStatus.state === "ready") &&
		workspaceQuery.isSuccess &&
		(usesPreviewWorkspaceData || !migrationOffer.isPending);

	useEffect(() => {
		let mounted = true;
		void aoBridge.appState.getFirstRunProjectPrompt()
			.then((next) => { if (mounted) setStatus(next); })
			.catch(() => { if (mounted) setStatus("seen"); });
		return () => { mounted = false; };
	}, []);

	const markSeen = () => {
		setStatus("seen");
		storeStep(null);
		void aoBridge.appState.markFirstRunProjectPromptSeen().catch(() => {
			// A failed write leaves the flag pending; the flow shows again next launch.
		});
	};

	useEffect(() => {
		if (status !== "pending" || !ready || open) return;
		if (projectCount > 0 || migrationOffer.data?.show) {
			markSeen();
			return;
		}
		setOpen(true);
	}, [migrationOffer.data?.show, open, projectCount, ready, status]);

	// Developer mode's Replay onboarding: start again from the first page,
	// whatever the first-run flag and the projects say.
	const replayRequest = useUiStore((state) => state.onboardingReplayRequest);
	const handledReplay = useRef(replayRequest);
	const replaySimulated = useRef(false);
	// Drop what was read before, so every check is asked again under the new answers.
	const resetToolChecks = () => {
		for (const queryKey of [systemRequirementsQueryKey, agentReadinessQueryKey, githubAuthRequirementQueryOptions.queryKey]) {
			void queryClient.resetQueries({ queryKey });
		}
	};
	useEffect(() => {
		if (replayRequest === handledReplay.current) return;
		handledReplay.current = replayRequest;
		storeStep(null);
		// A replay walks the flow as a new machine (dev builds only), so the
		// Tools page shows its installs instead of being skipped on a machine
		// that already has everything. It ends with the replay, unless the
		// Simulate a new machine switch was already on.
		if (!freshMachineSimActive()) {
			setFreshMachineSim(true, { gitInstalled: true });
			replaySimulated.current = freshMachineSimActive();
			resetToolChecks();
		}
		setOpen(true);
	}, [replayRequest]);

	if (!open) return null;
	return (
		<OnboardingFlow
			onFinish={() => {
				setOpen(false);
				markSeen();
				if (replaySimulated.current) {
					replaySimulated.current = false;
					setFreshMachineSim(false);
					resetToolChecks();
				}
				// The tour starts on Home, where projects are added, once it's
				// showing (not on whatever page was behind the onboarding).
				void navigate({ to: "/" }).then(startGuidedTour);
			}}
		/>
	);
}

// The person asked for a calmer press here than the app-wide shrink: buttons
// darken briefly and stay put while the next step fades in.
const PRESS_CLASS =
	"[&_button:active]:!scale-100 [&_button:active]:!translate-y-0 [&_button:not(:disabled):active]:brightness-[0.85]";

function OnboardingFlow({ onFinish }: { onFinish: () => void }) {
	const { t } = useTranslation();
	const [step, setStepState] = useState<Step>(readStoredStep);
	const setStep = (next: Step) => {
		storeStep(next);
		setStepState(next);
	};
	const index = STEPS.indexOf(step);
	const reduceMotion = useReducedMotion();
	// The window's width, for the copy column / stage split (stage from 900px).
	const rowRef = useRef<HTMLDivElement>(null);
	const [rowWidth, setRowWidth] = useState(0);
	useLayoutEffect(() => {
		const element = rowRef.current;
		if (!element) return;
		const measure = () => setRowWidth(element.clientWidth);
		measure();
		const observer = new ResizeObserver(measure);
		observer.observe(element);
		return () => observer.disconnect();
	}, []);
	const wide = rowWidth >= 900;
	// Pages with nothing to do are passed over in both directions, decided when
	// the person moves (these checks start on the welcome page, so they have
	// usually settled by then); a page still checking is shown, never flashed.
	const cloud = useCloudSession();
	const toolsGate = useSystemRequirementsGate();
	const ghAuth = useGitHubAuthRequirement();
	const agentReadiness = useAgentReadinessQuery();
	const ghReady = toolsGate.requirements.find((requirement) => requirement.id === "gh")?.satisfied === true && ghAuth.data?.satisfied === true;
	const agentReady = agentReadiness.data?.agents.some((agent) => agent.effectiveReadiness === "ready") ?? false;
	const skips = (candidate: Step) =>
		candidate === "signin" ? cloud.status === "authenticated"
		: candidate === "tools" ? ghReady && agentReady
		: false;
	// Past the last page with something to do, setup is over: the app takes
	// over with the guided tour.
	const goNext = () => {
		const next = STEPS.slice(index + 1).find((candidate) => !skips(candidate));
		if (next) setStep(next);
		else onFinish();
	};
	const goBack = () => {
		for (let i = index - 1; i >= 0; i--) {
			const previous = STEPS[i]!;
			if (i === 0 || !skips(previous)) {
				setStep(previous);
				return;
			}
		}
	};
	useOnboardingStep("welcome", step === "welcome");
	// The welcome story plays on its own and loops; each loop replays the chat.
	const [point, setPoint] = useState(0);
	const [cycle, setCycle] = useState(0);
	useEffect(() => {
		if (step !== "welcome") return;
		const timer = window.setTimeout(() => {
			if (point === ORCH_POINTS.length - 1) setCycle((current) => current + 1);
			setPoint((point + 1) % ORCH_POINTS.length);
		}, POINT_HOLD_MS[point]);
		return () => window.clearTimeout(timer);
	}, [step, point]);
	// The CI point sorts the board one pull request at a time, a beat apart, so
	// each trip to GitHub reads on its own; the last point settles the rest.
	// The beat is keyed to the loop and point it belongs to, so a new run starts
	// from 0 on its first frame instead of flashing the previous run's board.
	const sorting = step === "welcome" && point === CI_POINT;
	const sortKey = `${cycle}:${point}`;
	const [sort, setSort] = useState({ key: "", beat: 0 });
	useEffect(() => {
		if (!sorting) return;
		let beat = 0;
		const timer = window.setInterval(() => {
			beat += 1;
			setSort({ key: sortKey, beat });
			if (beat >= PR_SORTS) window.clearInterval(timer);
		}, SORT_BEAT_MS);
		return () => window.clearInterval(timer);
	}, [sorting, sortKey]);
	const sortBeat = !sorting ? SORT_ORDER.length : sort.key === sortKey ? sort.beat : 0;
	const githubShown = sorting;
	const toolAgents = useToolAgents();
	const scene = (
		<OnboardingScene
			step={step}
			point={point}
			cycle={cycle}
			sortBeat={sortBeat}
			pickerAgents={toolAgents.pickerAgents}
			selectedAgentId={toolAgents.selectedId}
			phoneShown={step === "signin"}
			phoneDelay={0}
			githubShown={githubShown}
		/>
	);

	return (
		// Back on every page but the welcome, which has nothing before it.
		<BackContext.Provider value={step === "welcome" ? undefined : goBack}>
		<div
			className={cn("first-run-onboarding onboarding-backdrop fixed inset-0 z-[calc(var(--z-overlay)-1)] flex flex-col bg-background text-foreground", PRESS_CLASS)}
			data-testid="first-run-onboarding"
			role="dialog"
			aria-modal="true"
			aria-label={t("firstRun.ariaLabel")}
		>
			{/* Window drag strip over the top edge; the macOS traffic lights sit in it. */}
			<div aria-hidden="true" className="absolute inset-x-0 top-0 z-10 h-toolbar" style={dragStyle} />
			{/* The copy changes per step; the preview stage on the left stays
			    mounted with one app window whose parts animate from step to step
			    (OnboardingScene), so nothing in it is ever swapped out whole. */}
			<div ref={rowRef} className="relative flex min-h-0 flex-1 flex-row-reverse overflow-hidden">
				<div className="relative z-10 min-h-0 shrink-0" style={{ width: wide ? COPY_WIDTH : "100%" }}>
					<AnimatePresence initial={false}>
						<motion.div
							key={step}
							className="absolute inset-0 flex"
							initial={reduceMotion ? false : { opacity: 0 }}
							animate={{ opacity: 1 }}
							// The outgoing copy leaves at once (the column is see-through in a
							// translucent window, so two steps' text must never overlap).
							exit={{ opacity: 0, transition: { duration: 0 } }}
							transition={FADE}
						>
							{step === "welcome" ? <WelcomeStep point={point} onNext={goNext} /> : null}
							{step === "signin" ? <SignInStep onNext={goNext} /> : null}
							{step === "tools" ? <ToolsStep onNext={goNext} /> : null}
						</motion.div>
					</AnimatePresence>
				</div>
				{wide ? (
					<div className="flex min-h-0 min-w-0 flex-1">
						{/* Beside the phone, the window drops transparency so both devices show
						    the theme in the same colours. */}
						<PreviewStage
							frameWidth={rowWidth - COPY_WIDTH}
						>
							{scene}
						</PreviewStage>
					</div>
				) : null}
			</div>
			{/* Progress floats over the bottom of the drawing; the actions sit under each step's copy. */}
			<div className="pointer-events-none absolute inset-x-0 bottom-0 z-20 flex h-14 items-center pl-12">
				<Progress index={index} />
			</div>
		</div>
		</BackContext.Provider>
	);
}

/** Quiet progress: one short segment per page and "Step 2 of 7". */
function Progress({ index }: { index: number }) {
	const { t } = useTranslation();
	return (
		<div className="flex items-center gap-3">
			<div aria-hidden="true" className="flex items-center gap-1">
				{STEPS.map((step, i) => (
					<span
						key={step}
						className={cn(
							"h-1 w-6 rounded-full transition-colors duration-normal",
							i <= index ? "bg-foreground" : "bg-muted",
						)}
					/>
				))}
			</div>
			<span className="sr-only">{t("firstRun.stepOf", { current: index + 1, total: STEPS.length })}</span>
		</div>
	);
}

const ActionSlotContext = createContext<HTMLDivElement | null>(null);
/** Portals the step's own actions into the action row under its copy, so they
 * lead the row wherever they're declared among the step's children. */
function StepAction({ children }: { children: ReactNode }) {
	const slot = useContext(ActionSlotContext);
	if (!slot) return null;
	return createPortal(children, slot);
}

/** Back, owned by the flow but drawn in each step's action row. */
const BackContext = createContext<(() => void) | undefined>(undefined);

/** The step's actions, just under its content and flush right: Back, then the
 * step's own, its primary last. */
function StepActions({ setSlot }: { setSlot: (slot: HTMLDivElement | null) => void }) {
	const { t } = useTranslation();
	const onBack = useContext(BackContext);
	return (
		<div className="flex flex-wrap items-start justify-end gap-2 pt-8 [&_button]:h-11 [&_button]:rounded-xl [&_button]:px-5 [&_button]:text-base">
			{onBack ? (
				<Button type="button" variant="ghost" className="!w-11 !px-0" aria-label={t("firstRun.back")} title={t("firstRun.back")} onClick={onBack}>
					<ArrowLeft className="!size-5" aria-hidden="true" />
				</Button>
			) : null}
			<div ref={setSlot} className="contents" />
		</div>
	);
}

/** A step's primary action: the label, then an arrow in its own rounded
 * square at the right end (a spinner there while it's busy). */
function NextButton({ busy = false, children, ...props }: ComponentProps<typeof Button> & { busy?: boolean }) {
	return (
		<Button variant="primary" {...props} className="!pl-5 !pr-1.5 gap-3">
			{children}
			<span aria-hidden="true" className="flex size-8 items-center justify-center rounded-lg bg-primary-foreground text-primary">
				{busy ? <LoaderCircle className="size-4 animate-spin" /> : <ArrowRight className="size-4" strokeWidth={2} />}
			</span>
		</Button>
	);
}

/** One page's copy and controls. The app preview beside it is the flow's one
 * persistent scene (OnboardingScene), driven by the step. */
function StepSplit({
	title,
	body,
	children,
}: {
	title: string;
	body?: ReactNode;
	children?: ReactNode;
}) {
	const [actionSlot, setActionSlot] = useState<HTMLDivElement | null>(null);
	return (
		// The copy and its actions sit centred in the column's height, a little
		// below the middle (more padding on top than under it). my-auto rather
		// than justify-center so a tall step still scrolls from its top.
		<div className="flex min-h-0 w-full flex-col overflow-y-auto bg-background pb-14 pl-12 pr-10 pt-[max(6rem,16vh)]">
			<div className="my-auto flex w-full max-w-md flex-col">
				<h1 className="text-balance text-heading-lg font-semibold leading-tight tracking-tight text-foreground">{title}</h1>
				{body ? <p className="max-w-sm pt-3 text-pretty text-sm leading-relaxed text-muted-foreground">{body}</p> : null}
				<ActionSlotContext.Provider value={actionSlot}>
					{children ? <div className="flex flex-col gap-4 pt-6">{children}</div> : null}
				</ActionSlotContext.Provider>
				<StepActions setSlot={setActionSlot} />
			</div>
		</div>
	);
}

function Waiting({ children }: { children: ReactNode }) {
	return (
		<p className="flex items-center gap-2 text-sm text-muted-foreground" role="status">
			<LoaderCircle className="size-icon-base animate-spin" aria-hidden="true" />
			{children}
		</p>
	);
}

/** A group of rows on a step: a short title and what the group is for. */
function RowGroup({ title, hint, children }: { title: string; hint: string; children: ReactNode }) {
	const id = useId();
	return (
		<section aria-labelledby={id} className="flex flex-col gap-2">
			<div className="flex flex-col gap-0.5 px-1">
				<h3 id={id} className="text-sm font-medium text-foreground">{title}</h3>
				<p className="text-xs text-muted-foreground">{hint}</p>
			</div>
			{children}
		</section>
	);
}

/** Muted trailing state for a row that needs nothing ("Installed", "Signed in"). */
function RowState({ children }: { children: ReactNode }) {
	return <span className="text-xs text-muted-foreground">{children}</span>;
}

function RowSpinner() {
	return <LoaderCircle className="size-icon-base animate-spin text-muted-foreground" aria-hidden="true" />;
}

/** One row of a shared list (settings card: filled, no outline, hairline
 * dividers): optional leading glyph, label, and a trailing state or the single
 * action that fixes it. */
/** The steps' row lists: an opaque surface with a hairline edge, so the
 * halftone wallpaper never shows through the rows. */
const ROW_LIST = "overflow-hidden rounded-xl border border-border bg-(--schematic-solid)";

function CheckRow({ icon, label, detail, trailing, below }: { icon?: ReactNode; label: ReactNode; detail?: ReactNode; trailing?: ReactNode; below?: ReactNode }) {
	return (
		<li className="px-4 py-3 [&+&]:border-t [&+&]:border-border">
			<div className="flex items-center gap-3">
				{icon ? <span className="flex shrink-0" aria-hidden="true">{icon}</span> : null}
				<span className="min-w-0 flex-1">
					<span className="block truncate text-sm text-foreground">{label}</span>
					{detail ? <span className="block truncate text-xs text-muted-foreground">{detail}</span> : null}
				</span>
				<span className="flex h-control-md shrink-0 items-center">{trailing}</span>
			</div>
			{/* Progress, or why an install didn't finish, under the label. */}
			{below ? <div className={cn("mt-2.5 flex flex-col gap-2", icon && "ps-7")}>{below}</div> : null}
		</li>
	);
}

/** Example work for the previews: clearly sample tasks, not the user's data. */
function useExampleTasks() {
	const { t } = useTranslation();
	return useMemo(() => [
		{
			id: "example-1",
			title: t("firstRun.exampleTask1"),
			provider: "claude-code",
			branch: "tests/sign-in",
			file: "signin.test.ts",
			diff: [["+", "it(\"signs in\", async () => {"], ["+", "  await signIn(user)"]],
			added: 48,
			removed: 0,
		},
		{
			id: "example-2",
			title: t("firstRun.exampleTask2"),
			provider: "codex",
			branch: "fix/auth-redirect",
			file: "redirect.ts",
			diff: [["-", "return \"/\""], ["+", "return next ?? home"]],
			added: 6,
			removed: 2,
			question: t("firstRun.syncQuestion"),
		},
		{
			id: "example-3",
			title: t("firstRun.exampleTask3"),
			provider: "claude-code",
			branch: "docs/readme",
			file: "README.md",
			diff: [["+", "## Getting started"], ["-", "TODO: setup steps"]],
			added: 12,
			removed: 3,
		},
		{
			id: "example-4",
			title: t("firstRun.exampleTask4"),
			provider: "codex",
			branch: "perf/search",
			file: "search.ts",
			diff: [["-", "items.filter(match)"], ["+", "index.query(term)"]],
			added: 31,
			removed: 9,
		},
	] as const, [t]);
}

/**
 * Where the example work stands at each beat of the story:
 * - none: nothing started yet (the person is still describing it);
 * - working: the orchestrator has started a worker on each task;
 * - sorted: the board has sorted them by what they need from you: one is
 *   approved and ready, one waits on your input, one is still going.
 */
type WorkStage = "none" | "working" | "sorted";

function exampleCards(tasks: ReturnType<typeof useExampleTasks>, stage: WorkStage, sortBeat: number = SORT_ORDER.length): PreviewCard[] {
	const [task1, task2, task3, task4] = tasks;
	if (stage === "none") return [];
	const working = (task: (typeof tasks)[number]): PreviewCard => ({ ...task, column: "building", status: "working", displayStatus: "Working", working: true, minutesAgo: 0 });
	if (stage === "working") return tasks.map(working);
	// Most of the work lands ready to merge; one change is in review and one
	// worker waits on you. Cards not yet reached keep working in Building.
	const settled: PreviewCard[] = [
		{ ...task1, column: "ready", status: "mergeable", displayStatus: "Mergeable", pr: 41, minutesAgo: 2 },
		{ ...task2, column: "building", status: "needs_input", minutesAgo: 4 },
		{ ...task3, column: "ready", status: "mergeable", displayStatus: "Mergeable", pr: 42, minutesAgo: 1 },
		{ ...task4, column: "needs_review", status: "review_pending", displayStatus: "Review pending", pr: 43, minutesAgo: 3 },
	];
	const reached = new Set<number>(SORT_ORDER.slice(0, sortBeat));
	return tasks.map((task, index) => (reached.has(index) ? settled[index]! : working(task)));
}

/**
 * The flow's one app window, drawn as a line drawing (OnboardingSchematic).
 * Its state comes from the step (and, on Welcome, the story's current part):
 * the pages cross-fade, session rows unfold in the sidebar, cards settle onto
 * the board and glide between lanes, and the agent picker opens.
 */
function OnboardingScene({
	step,
	point,
	cycle,
	sortBeat,
	pickerAgents,
	selectedAgentId,
	phoneShown,
	phoneDelay,
	githubShown,
}: {
	step: Step;
	point: number;
	cycle: number;
	sortBeat: number;
	pickerAgents: Array<{ id: string; label: string; dimmed?: boolean }>;
	selectedAgentId?: string;
	phoneShown: boolean;
	phoneDelay: number;
	githubShown: boolean;
}) {
	const { t } = useTranslation();
	const tasks = useExampleTasks();
	const project = t("firstRun.projectPlaceholder");
	const welcome = step === "welcome";
	const stage: WorkStage = welcome ? (point === 0 ? "none" : point === 1 ? "working" : "sorted") : "sorted";
	const page: ScenePage = welcome && point === 0 ? "chat" : step === "tools" ? "new" : "board";
	const cards = exampleCards(tasks, stage, welcome ? sortBeat : undefined);
	// While the chat leads a new loop, the board (fading out behind it) keeps
	// the last loop's work instead of flashing empty; the next loop's board
	// mounts fresh so its cards arrive again.
	const boardCards = stage === "none" ? exampleCards(tasks, "sorted") : cards;
	const boardKey = stage === "none" ? `${cycle}-previous` : `${cycle}`;
	const sidebarCards = cards
		.slice()
		.sort((a, b) => tasks.findIndex((task) => task.id === a.id) - tasks.findIndex((task) => task.id === b.id));
	return (
		<SchematicScene
			page={page}
			project={project}
			cards={boardCards}
			sidebarCards={sidebarCards}
			boardKey={boardKey}
			runId={cycle}
			pileCards={exampleCards(tasks, "working")}
			pickerAgents={pickerAgents}
			selectedAgentId={selectedAgentId}
			phone={<ExamplePhone />}
			phoneShown={phoneShown}
			phoneDelay={phoneDelay}
			githubShown={githubShown}
			focus={welcome ? STORY_FOCUS[point]! : step === "tools" ? "new" : step === "signin" ? "devices" : "overview"}
		/>
	);
}

/** What the camera frames through the welcome story: the request, the
 * workers at it, the CI round trips, then the whole board. */
const STORY_FOCUS: readonly SceneFocus[] = ["chat", "working", "ci", "overview"];

/** The same example work on the mobile app's Workers tab, as the story ends. */
function ExamplePhone() {
	const { t } = useTranslation();
	const [task1, task2, task3, task4] = useExampleTasks();
	return (
		<SchematicPhoneScreen
			project={t("firstRun.projectPlaceholder")}
			workers={[
				{ ...task2, status: "needs_input", minutesAgo: 4 },
				{ ...task3, status: "mergeable", pr: 42, minutesAgo: 1 },
				{ ...task1, status: "mergeable", pr: 41, minutesAgo: 2 },
				{ ...task4, status: "review_pending", pr: 43, minutesAgo: 3 },
			]}
		/>
	);
}

// ── 0. Welcome ──────────────────────────────────────────────────────────────

function WelcomeStep({ point, onNext }: { point: number; onNext: () => void }) {
	const { t } = useTranslation();
	const reduceMotion = useReducedMotion();
	return (
		<StepSplit
			title={t("firstRun.welcomeTitle")}
			body={t("firstRun.welcomeBody")}
		>
			{/* Narrates the preview: one caption for the part playing. */}
			<div className="flex flex-col gap-3 pt-6" aria-hidden="true">
				{/* Every caption stays mounted in one cell, so the block keeps the
				    tallest one's height; the outgoing one is gone before the next
				    comes in, so two never show at once. */}
				<div className="grid">
					{ORCH_POINTS.map(({ title, body }, i) => {
						const shown = i === point;
						return (
							<motion.div
								key={title}
								className="col-start-1 row-start-1 flex flex-col gap-1"
								initial={false}
								animate={shown ? { opacity: [0, 1], y: [8, 0] } : { opacity: 0, y: -8 }}
								transition={
									reduceMotion
										? { duration: 0 }
										: shown
											? { duration: 0.3, delay: CAPTION_OUT_S, ease: CAPTION_EASE }
											: { duration: CAPTION_OUT_S, ease: CAPTION_EASE }
								}
							>
								<span className="text-sm font-medium text-foreground">{t(title)}</span>
								<span className="max-w-sm text-pretty text-sm leading-relaxed text-muted-foreground">{t(body)}</span>
							</motion.div>
						);
					})}
				</div>
			</div>
			{/* The whole story for assistive tech, which skips the animation. */}
			<ol className="sr-only">
				{ORCH_POINTS.map(({ title, body }) => (
					<li key={title}>{t(title)}: {t(body)}</li>
				))}
			</ol>
			<StepAction>
				<NextButton onClick={onNext} autoFocus>{t("firstRun.getStarted")}</NextButton>
			</StepAction>
		</StepSplit>
	);
}

// ── 1. Sign in (optional) ───────────────────────────────────────────────────

/** Why an account is worth it: what only an account unlocks. */
const SIGN_IN_PERKS: Array<{ icon: LucideIcon; title: MessageKey; detail: MessageKey }> = [
	{ icon: Cloud, title: "firstRun.signInPerkCloud", detail: "firstRun.signInPerkCloudDetail" },
	{ icon: Smartphone, title: "firstRun.signInPerkMobile", detail: "firstRun.signInPerkMobileDetail" },
];

function SignInStep({ onNext }: { onNext: () => void }) {
	const { t } = useTranslation();
	const cloud = useCloudSession();
	const localAuth = useCloudLocalAuth();
	const { cloudEnabled } = useCloudGate();
	const openLocalSignIn = useLocalSignInDialogStore((state) => state.openDialog);
	const [waiting, setWaiting] = useState(false);
	const account = cloud.status === "authenticated" ? cloud.session : null;
	const accountName = account ? (account.user.displayName?.trim() || account.user.email) : "";
	const browserWaiting = waiting && !localAuth.available;
	const signIn = () => {
		if (localAuth.available) {
			openLocalSignIn();
			return;
		}
		// The hosted sign-in page opens in the browser; the session arrives
		// through cloud:sessionChanged and flips this page to the account row.
		setWaiting(true);
		cloud.signIn();
	};
	return (
		<StepSplit
			title={t("firstRun.signInTitle")}
			body={t("firstRun.signInBody")}
		>
			{cloud.status === "loading" ? (
				<Waiting>{t("firstRun.signInChecking")}</Waiting>
			) : account ? (
				<ul className={ROW_LIST}>
					<CheckRow
						label={accountName}
						detail={account.user.displayName ? account.user.email : undefined}
						trailing={<RowState>{t("firstRun.signedIn")}</RowState>}
					/>
				</ul>
			) : (
				<>
				<ul className={ROW_LIST}>
					{SIGN_IN_PERKS.map(({ icon: Icon, title, detail }) => (
						<CheckRow
							key={title}
							icon={<Icon className="size-icon-md text-muted-foreground" />}
							label={t(title)}
							detail={t(detail)}
						/>
					))}
				</ul>
				{/* While the browser has the login page, say so here; the actions
				    stay in the bottom bar. */}
				{browserWaiting ? (
					<div className="flex flex-wrap items-center gap-3">
						<Waiting>{t("firstRun.signInWaiting")}</Waiting>
						<Button variant="ghost" className="text-muted-foreground" onClick={() => cloud.signIn()}>
							{t("firstRun.signInReopen")}
						</Button>
					</div>
				) : null}
				</>
			)}
			{/* The local email/password dialog is only mounted by the cloud gate. */}
			{localAuth.available && !cloudEnabled ? <CloudLocalSignInDialog /> : null}
			{/* Continue once logged in, else Create account with a way to continue without one under it. */}
			<StepAction>
				{account ? (
					<NextButton onClick={onNext}>{t("firstRun.continue")}</NextButton>
				) : cloud.status === "loading" ? null : (
					<div className="flex flex-col items-center gap-2">
						<NextButton onClick={signIn} disabled={browserWaiting} busy={browserWaiting}>
							{t("firstRun.signInBrowser")}
						</NextButton>
						{/* A sentence, not a button, with the way out underlined and in the
						    body colour so it's plainly there without competing. */}
						<p className="text-sm text-muted-foreground">
							<Trans
								t={t}
								i18nKey="firstRun.doLater"
								components={{
									skip: (
										<button
											type="button"
											className="!h-auto !rounded-sm !px-0 !text-sm text-foreground underline decoration-foreground/40 underline-offset-4 transition-colors hover:decoration-foreground"
											onClick={onNext}
										/>
									),
								}}
							/>
						</p>
					</div>
				)}
			</StepAction>
		</StepSplit>
	);
}

// ── 2. Your tools: GitHub CLI and coding agents ─────────────────────────────



const AGENT_STATE_ORDER: Record<AgentRowState, number> = { ready: 0, signin: 1, checking: 2, missing: 3 };

/**
 * The agents the tools list shows (installed ones, plus the suggested ones to
 * install), ready first; and the New chat picker's view of them for the
 * preview: the ready ones, or until one is, the candidates dimmed.
 */
function useToolAgents() {
	const readiness = useAgentReadinessQuery();
	return useMemo(() => {
		const agents = (readiness.data?.agents ?? [])
			.filter((agent) => agent.installation.state !== "not_installed" || SUGGESTED_AGENTS.includes(agent.id))
			.map((agent) => ({ agent, state: agentRowState(agent) }))
			.sort((a, b) =>
				AGENT_STATE_ORDER[a.state] - AGENT_STATE_ORDER[b.state] ||
				(DEFAULT_AGENT_PRIORITY_RANK.get(a.agent.id) ?? 99) - (DEFAULT_AGENT_PRIORITY_RANK.get(b.agent.id) ?? 99) ||
				a.agent.label.localeCompare(b.agent.label))
			.slice(0, 6);
		const anyReady = agents.some((row) => row.state === "ready");
		const pickerAgents = (anyReady ? agents.filter((row) => row.state === "ready") : agents)
			.map(({ agent }) => ({ id: agent.id, label: agent.label, dimmed: !anyReady }));
		return { agents, anyReady, pickerAgents, selectedId: anyReady ? pickerAgents[0]?.id : undefined };
	}, [readiness.data]);
}

function ToolsStep({ onNext }: { onNext: () => void }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const gate = useSystemRequirementsGate();
	const ghAuth = useGitHubAuthRequirement();
	const readiness = useAgentReadinessQuery();
	const authPlans = useAgentAuthPlans();
	const startAuth = useStartAgentAuth();
	const [workflow, setWorkflow] = useState<AuthTerminalWorkflow | null>(null);
	const [authError, setAuthError] = useState<string | null>(null);
	const [checking, setChecking] = useState(false);

	const refresh = useCallback(async (agentIds: string[] = []) => {
		try {
			cacheAgentReadiness(queryClient, await ensureAgentReadiness(agentIds, "display"));
		} catch {
			await readiness.refetch();
		}
	}, [queryClient, readiness]);
	const ghInstall = useGhInstall(() => {
		void queryClient.invalidateQueries({ queryKey: systemRequirementsQueryKey });
		void ghAuth.refetch();
	});
	const agentInstalls = useAgentInstalls((agentId) => void refresh([agentId]));

	const gh = gate.requirements.find((requirement) => requirement.id === "gh");
	const ghInstalled = gh?.satisfied === true;
	const ghSignedIn = ghAuth.data?.satisfied === true;
	const ghChecking = gate.checking || ghAuth.isPending;
	const ghView = ghInstall.view;

	const { agents, anyReady } = useToolAgents();
	const signingInAgent = workflow?.agentId;
	const signingInReady = agents.some((row) => row.agent.id === signingInAgent && row.state === "ready");

	// While a login terminal is open, re-check that agent until it is ready,
	// then close the terminal on the user's behalf.
	useEffect(() => {
		if (!signingInAgent) return;
		if (signingInReady) {
			void closeAuthTerminal(workflow.terminal.handleId).catch(() => undefined);
			setWorkflow(null);
			return;
		}
		const timer = window.setInterval(() => void refresh([signingInAgent]), READINESS_POLL_MS);
		return () => window.clearInterval(timer);
	}, [refresh, signingInAgent, signingInReady, workflow]);

	const signIn = async (agentId: string) => {
		setAuthError(null);
		const plan = authPlans.data?.find((candidate) => candidate.agentId === agentId);
		if (plan?.launchMode === "documentation" && plan.documentationUrl) {
			await aoBridge.app.openExternal(plan.documentationUrl);
			return;
		}
		if (workflow) await closeAuthTerminal(workflow.terminal.handleId).catch(() => undefined);
		try {
			const result = await startAuth.mutateAsync(agentId);
			setWorkflow({
				agentId: agentId as AgentId,
				action: result.action,
				terminal: result.terminal,
				guidance: result.guidance ?? "",
				terminalInput: result.terminalInput,
				phase: "running",
				startedAt: Date.now(),
			});
		} catch (error) {
			setAuthError(error instanceof Error ? error.message : String(error));
		}
	};

	const closeLogin = () => {
		if (workflow) void closeAuthTerminal(workflow.terminal.handleId).catch(() => undefined);
		setWorkflow(null);
		void refresh();
	};

	const checkAgain = async () => {
		setChecking(true);
		try {
			await Promise.all([refresh(), queryClient.invalidateQueries({ queryKey: systemRequirementsQueryKey }), ghAuth.refetch()]);
		} finally {
			setChecking(false);
		}
	};

	return (
		<StepSplit
			title={t("firstRun.toolsTitle")}
			body={t("firstRun.toolsBody")}
		>
			{ghChecking || readiness.isPending ? (
				<Waiting>{t("firstRun.toolsChecking")}</Waiting>
			) : (
				<div className="flex flex-col gap-5">
					{/* GitHub is optional: agents still work locally without it. */}
					<RowGroup title={t("firstRun.toolsGithubHeading")} hint={t("firstRun.toolsGithubHint")}>
						<ul className={ROW_LIST}>
							<CheckRow
								icon={<GitHubIcon className="size-icon-base text-muted-foreground" />}
								label={t("firstRun.githubTitle")}
								detail={ghInstalled && !ghSignedIn ? t("firstRun.ghSignInHint") : undefined}
								trailing={ghView.busy ? <RowSpinner />
									: !ghInstalled && ghView.job?.status === "unsupported" ? null
									: !ghInstalled ? (
										<Button size="sm" variant="secondary" onClick={() => void ghInstall.start()}>
											{ghView.ended ? t("startup.installTryAgain") : t("firstRun.install")}
										</Button>
									)
									: ghSignedIn ? <RowState>{t("firstRun.signedIn")}</RowState>
									: null}
								below={ghView.busy && !ghInstalled ? <InstallProgress job={ghView.job} label={t("firstRun.githubTitle")} />
									: ghView.ended && !ghInstalled ? <InstallProblem view={ghView} docsUrl={GITHUB_CLI_URL} docsLabel={t("firstRun.ghDocs")} />
									: undefined}
							/>
						</ul>
						{/* The existing login flow: an embedded `gh auth login` terminal. */}
						{ghInstalled && !ghSignedIn ? <GitHubOnboardingNotice embedded /> : null}
					</RowGroup>
					<RowGroup title={t("firstRun.toolsAgentsHeading")} hint={t("firstRun.agentsBodyNone")}>
						<ul className={ROW_LIST}>
							{agents.map(({ agent, state }) => {
								const view = agentInstalls.view(agent.id);
								const plan = agentInstalls.planFor(agent.id);
								const canInstall = agentInstalls.canInstall(agent.id);
								const signingIn = signingInAgent === agent.id && !signingInReady;
								const missing = state === "missing";
								// Its plan says it can't be installed from here: say how instead. With no
								// plan (not loaded, or none for it) Install is tried, and says why if it fails.
								const manual = missing && plan !== undefined && !canInstall;
								return (
									<CheckRow
										key={agent.id}
										icon={<AgentAvatar provider={agent.id} className="size-icon-base" decorative />}
										label={agent.label}
										detail={manual && !view.ended ? plan?.reason : undefined}
										trailing={view.busy || signingIn ? <RowSpinner />
											: state === "ready" ? <RowState>{t("settings.harness.installed")}</RowState>
											: state === "signin" ? (
												<Button size="sm" variant="secondary" disabled={startAuth.isPending} onClick={() => void signIn(agent.id)}>{t("firstRun.signIn")}</Button>
											)
											: missing && !manual ? (
												<Button size="sm" variant="secondary" disabled={!agentInstalls.plansReady} onClick={() => void agentInstalls.start(agent.id)}>
													{view.ended ? t("startup.installTryAgain") : t("firstRun.install")}
												</Button>
											)
											: manual && plan?.documentationUrl ? <InstallDocsLink url={plan.documentationUrl} label={t("firstRun.installHowTo")} />
											: missing ? <RowState>{t("firstRun.agentMissing")}</RowState>
											: <RowSpinner />}
										below={view.busy && missing ? <InstallProgress job={view.job} label={agent.label} />
											: view.ended && missing ? <InstallProblem view={view} docsUrl={plan?.documentationUrl} />
											: undefined}
									/>
								);
							})}
						</ul>
					</RowGroup>
					<Button variant="ghost" size="sm" className="self-end text-muted-foreground" onClick={() => void checkAgain()} disabled={checking}>
						<RefreshCw className={cn("size-icon-md", checking && "animate-spin")} aria-hidden="true" />
						{t("firstRun.checkAgain")}
					</Button>
				</div>
			)}
			{workflow ? (
				<div className="overflow-hidden rounded-lg">
					<HarnessAuthTerminalPanel
						workflow={workflow}
						onClose={closeLogin}
						onRetry={() => void signIn(workflow.agentId)}
						onTerminalState={(state) => {
							if (state === "exited" || state === "error") void refresh([workflow.agentId]);
						}}
					/>
				</div>
			) : null}
			{authError ? <p className="text-xs text-destructive" role="alert">{authError}</p> : null}
			<StepAction>
				{/* At least one ready agent is needed to orchestrate anything; GitHub
				    stays optional (agents still work locally without it). */}
				<NextButton onClick={onNext} disabled={!anyReady}>{t("firstRun.continue")}</NextButton>
			</StepAction>
		</StepSplit>
	);
}
