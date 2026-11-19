import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { ArrowLeft, ArrowRight, ChevronDown, ChevronUp, Cloud, LoaderCircle, Monitor, RefreshCw, Smartphone, type AppIcon } from "@/components/app-icons";
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
	type AgentReadiness,
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
import { noteSignInSource } from "../../lib/cloud-telemetry";
import type { OnboardingStep } from "../../lib/onboarding-steps";
import {
	beginOnboardingRun,
	completeOnboardingRun,
	finishOnboardingAction,
	onboardingActionAttempt,
	onboardingActionPending,
	reportOnboardingBack,
	reportOnboardingStep,
	skipOnboardingStep,
	startOnboardingAction,
	supersedeOnboardingRun,
	useOnboardingStep,
} from "../../lib/onboarding-telemetry";
import type { OnboardingMissing, OnboardingRunKind } from "../../lib/onboarding-telemetry-schema";
import { usesPreviewWorkspaceData } from "../../lib/preview-mode";
import { useShell } from "../../lib/shell-context";
import { cn } from "../../lib/utils";
import { useLocalSignInDialogStore } from "../../stores/local-signin-dialog-store";
import { useUiStore } from "../../stores/ui-store";
import { STANDALONE_PROJECT_KIND } from "../../types/workspace";
import { AgentAvatar } from "../AgentAvatar";
import { CloudLocalSignInDialog } from "../CloudLocalSignInDialog";
import { GitHubIcon } from "../GitHubIcon";
import { FocusScope } from "@radix-ui/react-focus-scope";
import { Badge } from "../ui/badge";
import { ConfirmDialog } from "../ConfirmDialog";
import { GitHubLoginActions, GitHubLoginTerminal, useGitHubLogin } from "../GitHubOnboardingNotice";
import { dragStyle } from "../PageTopbar";
import { HarnessAuthTerminalPanel } from "../settings/harness/HarnessAuthTerminalPanel";
import { Button } from "../ui/button";
import { Pressable } from "../ui/pressable";
import { startGuidedTour } from "./GuidedTour";
import { agentRowState, closeLoginWorkflow, InstallDocsLink, InstallProblem, InstallProgress, useAgentInstalls, useGhInstall, useLoginLifetime, useOnboardingLogin, useVerifyWhileSigningIn, verifyAgentSignIn, type AgentRowState } from "./ToolInstalls";
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
/** Each page's step in the onboarding funnel (onboarding-steps.ts). */
const FUNNEL_STEP: Record<Step, OnboardingStep> = { welcome: "welcome", signin: "sign_in", tools: "tools" };
// Resume on the same page after a relaunch mid-setup (per-viewer convenience).
const STEP_STORAGE_KEY = "ao.firstRun.step";
// Always listed so a machine with no agents still has something to install.
const SUGGESTED_AGENTS = ["claude-code", "codex"];
// Where to get the GitHub CLI when it can't be installed from here.
const GITHUB_CLI_URL = "https://cli.github.com/";
// --duration-normal and --ease-out, for motion's JS-driven transitions.
const FADE = { duration: 0.15, ease: [0.23, 1, 0.32, 1] } as const;
/** The copy column's width beside the stage. */
const COPY_WIDTH = 520;

/** "How Orchestrator works": the three parts the welcome preview plays. */
const ORCH_POINTS = [
	{ title: "firstRun.orchPoint1Title", body: "firstRun.orchPoint1Body" },
	{ title: "firstRun.orchPoint2Title", body: "firstRun.orchPoint2Body" },
	{ title: "firstRun.orchPointCiTitle", body: "firstRun.orchPointCiBody" },
] as const;
/** The part where pull requests go through CI and code review on GitHub. */
const CI_POINT = 2;
/** How far the welcome's other story points recede while one plays. */
const INACTIVE_POINT_OPACITY = 0.35;
/** The welcome's story points come up and recede on this curve. */
const CAPTION_EASE = [0.23, 1, 0.32, 1] as const;
/** How long each part of the story holds before the next plays. */
const POINT_HOLD_MS = [4200, 3800, 10200] as const;
/** The order the example work settles (task indexes), a beat apart, in the CI
 * part: first the three with pull requests, each beat long enough for its
 * round trip to GitHub (OnboardingSchematic), then the one waiting on you, the
 * board held a moment once it's all sorted. */
const SORT_ORDER = [3, 2, 0, 1] as const;
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
			// A run left open on an earlier launch ends here, for the person.
			supersedeOnboardingRun();
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
	// A replay is its own run in the funnel (run_kind: replay).
	const runKind = useRef<OnboardingRunKind>("first_run");
	// Drop what was read before, so every check is asked again under the new answers.
	const resetToolChecks = () => {
		for (const queryKey of [systemRequirementsQueryKey, agentReadinessQueryKey, githubAuthRequirementQueryOptions.queryKey]) {
			void queryClient.resetQueries({ queryKey });
		}
	};
	useEffect(() => {
		if (replayRequest === handledReplay.current) return;
		handledReplay.current = replayRequest;
		runKind.current = "replay";
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
			runKind={runKind.current}
			onFinish={({ tour = true } = {}) => {
				completeOnboardingRun(tour ? "finished" : "skipped_setup");
				setOpen(false);
				markSeen();
				if (replaySimulated.current) {
					replaySimulated.current = false;
					setFreshMachineSim(false);
					resetToolChecks();
				}
				// The tour starts on Home, where projects are added, once it's
				// showing (not on whatever page was behind the onboarding). A
				// skipped setup has no harness to show it with, so it goes without.
				const replay = runKind.current === "replay";
				if (tour) void navigate({ to: "/" }).then(() => startGuidedTour({ replay }));
			}}
		/>
	);
}

// The person asked for a calmer press here than the app-wide shrink: buttons
// darken briefly and stay put while the next step fades in.
const PRESS_CLASS =
	"[&_button:active]:!scale-100 [&_button:active]:!translate-y-0 [&_button:not(:disabled):active]:brightness-[0.85]";

function OnboardingFlow({ runKind, onFinish }: { runKind: OnboardingRunKind; onFinish: (options?: { tour?: boolean }) => void }) {
	const { t } = useTranslation();
	// Settings open over the app (a harness's installers, from Your tools).
	const settingsAside = useUiStore((state) => state.settingsModal?.scope === "global");
	const [step, setStepState] = useState<Step>(readStoredStep);
	const setStep = (next: Step) => {
		storeStep(next);
		setStepState(next);
	};
	const index = STEPS.indexOf(step);
	// Started, or resumed on a later launch, before any page reports its view.
	const openedOn = useRef(step);
	useLayoutEffect(() => beginOnboardingRun(runKind, FUNNEL_STEP[openedOn.current]), [runKind]);
	const reduceMotion = useReducedMotion();
	const harnessLogin = useOnboardingLogin();
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
	// over with the guided tour. `skipped`: the person moved on without doing
	// the page (Sign in's "do it later").
	const goNext = ({ skipped = false }: { skipped?: boolean } = {}) => {
		if (skipped) skipOnboardingStep(FUNNEL_STEP[step], "user");
		else reportOnboardingStep(FUNNEL_STEP[step], "completed", pageCompletion());
		const passed: Step[] = [];
		const next = STEPS.slice(index + 1).find((candidate) => {
			if (!skips(candidate)) return true;
			passed.push(candidate);
			return false;
		});
		for (const candidate of passed) skipOnboardingStep(FUNNEL_STEP[candidate], "already_done");
		if (next) setStep(next);
		else onFinish();
	};
	const goBack = () => {
		for (let i = index - 1; i >= 0; i--) {
			const previous = STEPS[i]!;
			if (i === 0 || !skips(previous)) {
				reportOnboardingBack(FUNNEL_STEP[step], FUNNEL_STEP[previous]);
				setStep(previous);
				return;
			}
		}
	};
	// Pages report their view here and their completion from goNext, never on
	// leaving, since Back leaves a page without finishing it.
	// Unknown (not sent) until the check answers, rather than a false 0.
	// A harness still being checked doesn't count as not ready (as in useAgentInventoryTelemetry).
	const readinessSettled = agentReadiness.data?.agents.every((agent) => agent.installation.freshness !== "checking" && agent.authentication.freshness !== "checking");
	const readyAgentCount = readinessSettled ? agentReadiness.data?.agents.filter((agent) => agent.effectiveReadiness === "ready").length : undefined;
	const ghInstalled = toolsGate.requirements.find((requirement) => requirement.id === "gh")?.satisfied;
	const toolsMissing = (): string | undefined => {
		const agents = agentReadiness.data?.agents;
		const missing: OnboardingMissing[] = [];
		if (ghInstalled === false) missing.push("gh");
		if (ghInstalled === true && ghAuth.data?.satisfied === false) missing.push("gh_auth");
		if (agents && !agents.some((agent) => agent.installation.state === "installed")) missing.push("agent");
		else if (agents && !agentReady) missing.push("agent_auth");
		return missing.join(",") || undefined;
	};
	useOnboardingStep("welcome", step === "welcome", { autoComplete: false });
	useOnboardingStep("sign_in", step === "signin", { autoComplete: false });
	useOnboardingStep("tools", step === "tools", {
		autoComplete: false,
		viewed: step === "tools" ? { missing: toolsMissing(), agents_ready: readyAgentCount } : undefined,
	});
	// A browser sign-in started on the Sign in page can come back after the
	// person moved on; it still ends that attempt.
	useEffect(() => {
		if (cloud.status === "authenticated") finishOnboardingAction("cloud_sign_in", "succeeded");
	}, [cloud.status]);
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
	// The CI point sorts the board one card at a time, a beat apart, so each
	// trip to GitHub reads on its own.
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
			if (beat >= SORT_ORDER.length) window.clearInterval(timer);
		}, SORT_BEAT_MS);
		return () => window.clearInterval(timer);
	}, [sorting, sortKey]);
	const sortBeat = !sorting ? SORT_ORDER.length : sort.key === sortKey ? sort.beat : 0;
	const githubShown = sorting;
	// What the funnel records about a page as it's finished.
	function pageCompletion(): Record<string, unknown> {
		if (step === "welcome") return { story_points_seen: cycle > 0 ? ORCH_POINTS.length : point + 1, story_completed: cycle > 0 };
		if (step === "tools") return { agents_ready: readyAgentCount, gh_ready: ghReady };
		return {};
	}
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
		{/* A modal: Tab and Shift+Tab stay inside it (the shell behind is inert,
		    see _shell.tsx), except while Settings has it step aside. */}
		<FocusScope asChild loop trapped={!settingsAside}>
		<div
			className={cn("first-run-onboarding onboarding-backdrop fixed inset-0 z-[calc(var(--z-overlay)-1)] flex flex-col bg-background text-foreground", PRESS_CLASS, settingsAside && "hidden")}
			data-testid="first-run-onboarding"
			// Settings opened from here (a harness's installers): step aside, keeping the page.
			data-aside={settingsAside ? "" : undefined}
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
							{step === "welcome" ? <WelcomeStep point={point} onNext={() => goNext()} /> : null}
							{step === "signin" ? <SignInStep onNext={() => goNext()} onSkip={() => goNext({ skipped: true })} /> : null}
							{step === "tools" ? (
								<ToolsStep
									onNext={() => goNext()}
									onSkip={() => {
										skipOnboardingStep("tools", "user");
										onFinish({ tour: false });
									}}
									login={harnessLogin}
								/>
							) : null}
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
		</FocusScope>
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
 * step's own, its primary last. On a step taller than the window they stay in
 * view at the column's foot while the content scrolls under them. */
function StepActions({ setSlot }: { setSlot: (slot: HTMLDivElement | null) => void }) {
	const { t } = useTranslation();
	const onBack = useContext(BackContext);
	return (
		<div className="sticky bottom-0 z-10 flex flex-wrap items-start justify-end gap-2 bg-background pb-14 pt-8 [&_button]:h-11 [&_button]:rounded-xl [&_button]:px-5 [&_button]:text-base">
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
	titleHidden = false,
	body,
	children,
}: {
	title: string;
	/** Kept for assistive tech only: the step's content says it on its own. */
	titleHidden?: boolean;
	body?: ReactNode;
	children?: ReactNode;
}) {
	const [actionSlot, setActionSlot] = useState<HTMLDivElement | null>(null);
	return (
		// The copy and its actions sit centred in the column's height, a little
		// below the middle (more padding on top than under it). my-auto rather
		// than justify-center so a tall step still scrolls from its top.
		// No bottom padding here: the actions carry it, so when they stick to the
		// foot of a tall step nothing shows beneath them.
		<div className="flex min-h-0 w-full flex-col overflow-y-auto bg-background pl-12 pr-10 pt-[max(6rem,16vh)]">
			<div className="my-auto flex w-full max-w-md flex-col">
				<h1 className={titleHidden ? "sr-only" : "text-balance text-heading-lg font-semibold leading-tight tracking-tight text-foreground"}>{title}</h1>
				{body ? <p className="max-w-sm pt-3 text-pretty text-sm leading-relaxed text-muted-foreground">{body}</p> : null}
				<ActionSlotContext.Provider value={actionSlot}>
					{children ? <div className={cn("flex flex-col gap-4", !titleHidden && "pt-6")}>{children}</div> : null}
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
function RowGroup({ title, hint, action, children }: { title: string; hint: string; action?: ReactNode; children: ReactNode }) {
	const id = useId();
	return (
		<section aria-labelledby={id} className="flex flex-col gap-2">
			<div className="flex items-center gap-2 px-1">
				<div className="flex min-w-0 flex-1 flex-col gap-0.5">
					<h3 id={id} className="text-sm font-medium text-foreground">{title}</h3>
					<p className="text-xs text-muted-foreground">{hint}</p>
				</div>
				{action}
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
/** CheckRows as an open list, without the box or the lines between rows. */
const OPEN_ROW_LIST = "[&>li]:!border-t-0 [&>li]:!px-0";

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

/** Harnesses whose marks stand for "N more" (well-known ones, one per brand). */
const MORE_HARNESS_MARKS = ["cursor", "gemini", "copilot", "opencode", "aider", "amp"];

/** The last row of the harness list: three well-known marks and how many more
 * harnesses AO can install, opening the full list. */
function MoreHarnessesRow({ ids, onShow }: { ids: string[]; onShow: () => void }) {
	const { t } = useTranslation();
	const marks = [...MORE_HARNESS_MARKS.filter((id) => ids.includes(id)), ...ids.filter((id) => !MORE_HARNESS_MARKS.includes(id))].slice(0, 3);
	return (
		<li className="[&+&]:border-t [&+&]:border-border">
			<Pressable
				className="flex w-full items-center gap-3 px-4 py-3 transition-colors duration-fast hover:bg-muted/60 focus-visible:bg-muted/60"
				aria-expanded={false}
				onClick={onShow}
			>
				<span className="flex shrink-0 -space-x-1.5" aria-hidden="true">
					{/* Each mark sits over the one after it. */}
					{marks.map((id, index) => (
						<span key={id} className="relative grid size-5 place-items-center rounded-full bg-(--schematic-solid) ring-2 ring-(--schematic-solid)" style={{ zIndex: marks.length - index }}>
							<AgentAvatar provider={id} className="size-icon-base" decorative />
						</span>
					))}
				</span>
				<span className="min-w-0 flex-1 truncate text-sm text-foreground">{t("firstRun.moreHarnesses", { count: ids.length })}</span>
				<span className="flex shrink-0 items-center gap-1 text-xs text-muted-foreground">
					{t("firstRun.showAllHarnesses")}
					<ChevronDown className="size-icon-sm" aria-hidden="true" />
				</span>
			</Pressable>
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
 * workers at it, then the CI round trips. */
const STORY_FOCUS: readonly SceneFocus[] = ["chat", "working", "ci"];

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
			{/* Narrates the preview: the whole story at once, the part playing
			    brought up and the rest dimmed. */}
			<ol className="flex flex-col gap-4 pt-6 pb-8">
				{ORCH_POINTS.map(({ title, body }, i) => {
					const active = i === point;
					return (
						<motion.li
							key={title}
							className="flex flex-col gap-1"
							aria-current={active ? "step" : undefined}
							initial={false}
							animate={{ opacity: active ? 1 : INACTIVE_POINT_OPACITY }}
							transition={reduceMotion ? { duration: 0 } : { duration: 0.4, ease: CAPTION_EASE }}
						>
							<span className="text-sm font-medium text-foreground">{t(title)}</span>
							<span className="max-w-sm text-pretty text-sm leading-relaxed text-muted-foreground">{t(body)}</span>
						</motion.li>
					);
				})}
			</ol>
			<StepAction>
				<NextButton onClick={onNext} autoFocus>{t("firstRun.getStarted")}</NextButton>
			</StepAction>
		</StepSplit>
	);
}

// ── 1. Sign in (optional) ───────────────────────────────────────────────────

/** Why an account is worth it: what only an account unlocks. */
const SIGN_IN_PERKS: Array<{ icon: AppIcon; title: MessageKey; detail: MessageKey; pro?: boolean }> = [
	{ icon: Smartphone, title: "firstRun.signInPerkMobile", detail: "firstRun.signInPerkMobileDetail" },
	{ icon: Monitor, title: "firstRun.signInPerkRemote", detail: "firstRun.signInPerkRemoteDetail" },
	// Cloud sandboxes come with a paid plan.
	{ icon: Cloud, title: "firstRun.signInPerkCloud", detail: "firstRun.signInPerkCloudDetail", pro: true },
];

function SignInStep({ onNext, onSkip }: { onNext: () => void; onSkip: () => void }) {
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
		// cloud_signed_in reads where the sign-in started (source: onboarding).
		noteSignInSource("onboarding");
		startOnboardingAction("cloud_sign_in", { step: "sign_in" });
		if (localAuth.available) {
			openLocalSignIn();
			return;
		}
		// The hosted sign-in page opens in the browser; the session arrives
		// through cloud:sessionChanged and flips this page to the account row.
		setWaiting(true);
		// As cloud.signIn, but the browser failing to open ends the attempt.
		void aoBridge.cloud.signIn().catch(() => {
			finishOnboardingAction("cloud_sign_in", "failed", { reason: "start_failed" });
		});
	};
	return (
		<StepSplit
			title={t("firstRun.signInTitle")}
			body={t("firstRun.signInBody")}
		>
			{cloud.status === "loading" ? (
				<Waiting>{t("firstRun.signInChecking")}</Waiting>
			) : account ? (
				<ul className={OPEN_ROW_LIST}>
					<CheckRow
						label={accountName}
						detail={account.user.displayName ? account.user.email : undefined}
						trailing={<RowState>{t("firstRun.signedIn")}</RowState>}
					/>
				</ul>
			) : (
				<>
				<ul className={OPEN_ROW_LIST}>
					{SIGN_IN_PERKS.map(({ icon: Icon, title, detail, pro }) => (
						<CheckRow
							key={title}
							icon={<Icon className="size-icon-md text-muted-foreground" />}
							label={
								<span className="inline-flex items-center gap-1.5">
									{t(title)}
									{pro ? <Badge variant="outline" className="h-4 px-1.5 text-2xs">{t("firstRun.proBadge")}</Badge> : null}
								</span>
							}
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
											onClick={() => {
												// Moving on while the browser still has the sign-in page.
												finishOnboardingAction("cloud_sign_in", "cancelled");
												onSkip();
											}}
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
		const rows = (readiness.data?.agents ?? [])
			.map((agent) => ({ agent, state: agentRowState(agent) }))
			.sort((a, b) =>
				AGENT_STATE_ORDER[a.state] - AGENT_STATE_ORDER[b.state] ||
				(DEFAULT_AGENT_PRIORITY_RANK.get(a.agent.id) ?? 99) - (DEFAULT_AGENT_PRIORITY_RANK.get(b.agent.id) ?? 99) ||
				a.agent.label.localeCompare(b.agent.label));
		const agents = rows
			.filter(({ agent }) => agent.installation.state !== "not_installed" || SUGGESTED_AGENTS.includes(agent.id))
			.slice(0, 6);
		// Every other harness AO knows, behind "N more harnesses".
		const shown = new Set(agents.map(({ agent }) => agent.id));
		const more = rows.filter(({ agent }) => !shown.has(agent.id));
		const anyReady = agents.some((row) => row.state === "ready");
		const pickerAgents = (anyReady ? agents.filter((row) => row.state === "ready") : agents)
			.map(({ agent }) => ({ id: agent.id, label: agent.label, dimmed: !anyReady }));
		return { agents, more, anyReady, pickerAgents, selectedId: anyReady ? pickerAgents[0]?.id : undefined };
	}, [readiness.data]);
}

function ToolsStep({ onNext, onSkip, login }: {
	onNext: () => void;
	onSkip: () => void;
	/** The harness login in progress, kept by the flow so it survives Back (useOnboardingLogin). */
	login: ReturnType<typeof useOnboardingLogin>;
}) {
	const [workflow, setWorkflow] = login;
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const gate = useSystemRequirementsGate();
	const ghAuth = useGitHubAuthRequirement();
	const readiness = useAgentReadinessQuery();
	const authPlans = useAgentAuthPlans();
	const startAuth = useStartAgentAuth();
	const [authError, setAuthError] = useState<string | null>(null);
	const [checking, setChecking] = useState(false);

	const refresh = useCallback(async (agentIds: string[] = []) => {
		try {
			cacheAgentReadiness(queryClient, await ensureAgentReadiness(agentIds, "display"));
		} catch {
			await readiness.refetch();
		}
	}, [queryClient, readiness]);
	// Readiness reuses sign-in observations for a while, so a login that just
	// finished can still read as signed out. Where the answer must be current
	// (a login's terminal ended, or the person asked to check again), make the
	// daemon verify sign-in first, as Settings > Harness does, then read again.
	const verify = useCallback((agentIds: string[]) => verifyAgentSignIn(queryClient, agentIds, refresh), [queryClient, refresh]);
	const ghInstall = useGhInstall(() => {
		void queryClient.invalidateQueries({ queryKey: systemRequirementsQueryKey });
		void ghAuth.refetch();
	});
	const agentInstalls = useAgentInstalls((agentId) => void refresh([agentId]));
	// Signing the CLI in once it's installed: actions on the row, terminal under it.
	const ghLogin = useGitHubLogin();

	const gh = gate.requirements.find((requirement) => requirement.id === "gh");
	const ghInstalled = gh?.satisfied === true;
	const ghSignedIn = ghAuth.data?.satisfied === true;
	const ghChecking = gate.checking || ghAuth.isPending;
	const ghView = ghInstall.view;

	const { agents, more, anyReady } = useToolAgents();
	const [confirmingSkip, setConfirmingSkip] = useState(false);
	// A harness's own installers (every method, diagnostics) live in Settings >
	// Harness; onboarding steps aside while Settings is open and comes back here.
	const openGlobalSettings = useUiStore((state) => state.openGlobalSettings);
	const openHarnessSettings = (agentId: string, job?: { startedAt?: string | null; error?: string }) =>
		openGlobalSettings("harness", {
			focusAgentId: agentId,
			showDiagnostics: job ? { jobStartedAt: job.startedAt ?? undefined, error: job.error } : undefined,
		});
	const settingsOpen = useUiStore((state) => state.settingsModal?.scope === "global");
	const wasInSettings = useRef(false);
	const [showAllAgents, setShowAllAgents] = useState(false);
	// A login in progress (not one that timed out or is being cleaned up).
	const signingInAgent = workflow?.phase === "running" ? workflow.agentId : undefined;
	// Any harness can be signed in to here, the ones under "more" included.
	const signingInReady = [...agents, ...more].some((row) => row.agent.id === signingInAgent && row.state === "ready");

	// While a login terminal is open, verify that agent's sign-in until it is
	// ready (a login can finish while its terminal stays open), then close the
	// terminal on the user's behalf.
	useVerifyWhileSigningIn(signingInAgent, signingInReady, verify);
	useEffect(() => {
		if (!signingInAgent || !signingInReady || !workflow) return;
		void closeLoginWorkflow(workflow, setWorkflow, t("settings.harness.authFailed"));
	}, [signingInAgent, signingInReady, workflow, setWorkflow, t]);

	const signIn = async (agentId: string) => {
		setAuthError(null);
		const plan = authPlans.data?.find((candidate) => candidate.agentId === agentId);
		startOnboardingAction("agent_sign_in", { target: agentId, step: "tools" });
		if (plan?.launchMode === "documentation" && plan.documentationUrl) {
			// Signs in outside AO: its guide opens, and the attempt ends here.
			finishOnboardingAction("agent_sign_in", "handed_off", { target: agentId, reason: "documentation" });
			await aoBridge.app.openExternal(plan.documentationUrl);
			return;
		}
		// The daemon allows one login at a time: a previous one must close first.
		if (workflow && !(await closeLoginWorkflow(workflow, setWorkflow, t("settings.harness.authFailed")))) {
			finishOnboardingAction("agent_sign_in", "failed", { target: agentId, reason: "cleanup_failed" });
			return;
		}
		// Another harness's login was open; it ends here unless it already signed in.
		if (workflow && workflow.agentId !== agentId) settleSignIn(workflow.agentId, "cancelled");
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
			finishOnboardingAction("agent_sign_in", "failed", { target: agentId, reason: "start_failed" });
			setAuthError(error instanceof Error ? error.message : String(error));
		}
	};
	// Once a verify has settled, a sign-in still pending ends as signed in or not.
	// `attempt`: the one the verify was started for, so a newer one is left alone.
	const settleSignIn = (agentId: string, notReady: "cancelled" | "failed", attempt?: number) => {
		if (!onboardingActionPending("agent_sign_in", agentId)) return;
		const ready = queryClient.getQueryData<AgentReadiness>(agentReadinessQueryKey)?.agents
			.some((agent) => agent.id === agentId && agent.effectiveReadiness === "ready");
		if (ready) finishOnboardingAction("agent_sign_in", "succeeded", { target: agentId, attempt });
		else finishOnboardingAction("agent_sign_in", notReady, { target: agentId, attempt, reason: notReady === "failed" ? "not_signed_in" : undefined });
	};
	// A harness that turns ready ends its sign-in, wherever the login finished.
	const readyIds = [...agents, ...more].filter((row) => row.state === "ready").map((row) => row.agent.id).join(",");
	useEffect(() => {
		for (const agentId of readyIds.split(",")) if (agentId) finishOnboardingAction("agent_sign_in", "succeeded", { target: agentId });
	}, [readyIds]);
	const loginPhase = workflow?.phase;
	const loginAgent = workflow?.agentId;
	useEffect(() => {
		if (!loginAgent) return;
		if (loginPhase === "timed_out") finishOnboardingAction("agent_sign_in", "timed_out", { target: loginAgent });
		if (loginPhase === "cleanup_failed") finishOnboardingAction("agent_sign_in", "failed", { target: loginAgent, reason: "cleanup_failed" });
	}, [loginPhase, loginAgent]);

	// Close the terminal first, then verify that harness afresh, so the row
	// settles as soon as Close returns instead of on the next 5 s check.
	const closeLogin = async () => {
		if (!workflow) {
			void refresh();
			return;
		}
		const attempt = onboardingActionAttempt("agent_sign_in", workflow.agentId);
		if (await closeLoginWorkflow(workflow, setWorkflow, t("settings.harness.authFailed"))) {
			await verify([workflow.agentId]);
			// Closed by the person before the harness was signed in.
			if (attempt !== undefined) settleSignIn(workflow.agentId, "cancelled", attempt);
		}
	};
	useLoginLifetime(workflow, setWorkflow, { timedOut: t("settings.harness.authTimedOut"), failed: t("settings.harness.authFailed") }, (agentId) => void verify([agentId]));

	// A harness from the full list that's installed, waiting to be signed in,
	// installing or failed joins the main list, so it never hides behind
	// "N more" (or scrolls out of sight in the full list).
	// (One still being checked stays where it is, so the list doesn't swell
	// while the daemon looks over every harness.)
	const pinned = more.filter(({ agent, state }) => {
		const view = agentInstalls.view(agent.id);
		return state === "ready" || state === "signin" || view.busy || view.ended;
	});
	const pinnedIds = new Set(pinned.map(({ agent }) => agent.id));
	const listed = [...agents, ...pinned];
	const rest = more.filter(({ agent }) => !pinnedIds.has(agent.id));
	const agentRow = ({ agent, state }: (typeof agents)[number]) => {
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
				trailing={view.busy ? <InstallProgress compact job={view.job} label={agent.label} />
					: signingIn ? <RowSpinner />
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
				// Installing shows as a short bar on the row, as for the GitHub CLI;
				// a failed install says why.
				below={view.ended && missing ? (
						<InstallProblem view={view} docsUrl={plan?.documentationUrl} otherWays={() => openHarnessSettings(agent.id, view.job)} />
					)
					: undefined}
			/>
		);
	};

	const checkAgain = async () => {
		setChecking(true);
		try {
			// Installed harnesses still waiting on sign-in are verified afresh.
			const awaitingSignIn = [...agents, ...more].filter((row) => row.state === "signin").map((row) => row.agent.id);
			await Promise.all([
				awaitingSignIn.length ? verify(awaitingSignIn) : refresh(),
				queryClient.invalidateQueries({ queryKey: systemRequirementsQueryKey }),
				ghAuth.refetch(),
			]);
		} finally {
			setChecking(false);
		}
	};
	// Back from Settings: whatever was installed or signed in there shows here.
	const checkAgainRef = useRef(checkAgain);
	checkAgainRef.current = checkAgain;
	useEffect(() => {
		if (settingsOpen) wasInSettings.current = true;
		else if (wasInSettings.current) {
			wasInSettings.current = false;
			void checkAgainRef.current();
		}
	}, [settingsOpen]);

	return (
		<StepSplit
			title={t("firstRun.toolsTitle")}
			// The Tools and Harnesses groups say what this page is for.
			titleHidden
		>
			{ghChecking || readiness.isPending ? (
				<Waiting>{t("firstRun.toolsChecking")}</Waiting>
			) : (
				<div className="flex flex-col gap-5">
					{/* GitHub first; it stays optional (agents still work locally without it). */}
					<RowGroup title={t("firstRun.toolsGithubHeading")} hint={t("firstRun.toolsGithubHint")}>
						<ul className={ROW_LIST}>
							<CheckRow
								icon={<GitHubIcon className="size-icon-base text-muted-foreground" />}
								label={t("firstRun.githubTitle")}
								trailing={ghView.busy ? <InstallProgress compact job={ghView.job} label={t("firstRun.githubTitle")} />
									: !ghInstalled && ghView.job?.status === "unsupported" ? null
									: !ghInstalled ? (
										<Button size="sm" variant="secondary" onClick={() => void ghInstall.start()}>
											{ghView.ended ? t("startup.installTryAgain") : t("firstRun.install")}
										</Button>
									)
									: ghSignedIn ? <RowState>{t("firstRun.signedIn")}</RowState>
									: <GitHubLoginActions login={ghLogin} />}
								// Installing shows as a short bar on the row; a failed install says why.
								below={ghView.ended && !ghInstalled ? <InstallProblem view={ghView} docsUrl={GITHUB_CLI_URL} docsLabel={t("firstRun.ghDocs")} />
									// Installed but signed out: the `gh auth login` terminal Sign in
									// with GitHub opens, in the card.
									: ghInstalled && !ghSignedIn && (ghLogin.terminal || ghLogin.startError) ? <GitHubLoginTerminal login={ghLogin} />
									: undefined}
							/>
						</ul>
					</RowGroup>
					<RowGroup
						title={t("firstRun.toolsAgentsHeading")}
						hint={t("firstRun.agentsBodyNone")}
						action={
							// Checks every tool and harness again (one installed outside AO, say).
							<Button
								type="button"
								variant="quiet"
								size="icon-sm"
								aria-label={t("firstRun.checkAgain")}
								title={t("firstRun.checkAgain")}
								onClick={() => void checkAgain()}
								disabled={checking}
							>
								<RefreshCw className={cn("size-icon-md", checking && "animate-spin")} aria-hidden="true" />
							</Button>
						}
					>
						<ul className={ROW_LIST}>
							{listed.map(agentRow)}
							{rest.length > 0 && !showAllAgents ? (
								<MoreHarnessesRow ids={rest.map(({ agent }) => agent.id)} onShow={() => setShowAllAgents(true)} />
							) : null}
						</ul>
						{showAllAgents && rest.length > 0 ? (
							<>
								{/* Every other harness, installable from here too; scrolls past a few rows. */}
								<ul className={cn(ROW_LIST, "max-h-80 overflow-y-auto")} aria-label={t("firstRun.moreHarnessesList")}>
									{rest.map(agentRow)}
								</ul>
								<Button type="button" variant="ghost" size="sm" className="self-start text-muted-foreground" onClick={() => setShowAllAgents(false)}>
									{t("firstRun.showFewerHarnesses")}
									<ChevronUp aria-hidden="true" />
								</Button>
							</>
						) : null}
					</RowGroup>
				</div>
			)}
			{workflow ? (
				<div className="overflow-hidden rounded-lg">
					<HarnessAuthTerminalPanel
						workflow={workflow}
						onClose={() => void closeLogin()}
						onRetry={() => void signIn(workflow.agentId)}
						onTerminalState={(state) => {
							if (state === "exited" || state === "error") {
								const agentId = workflow.agentId;
								const attempt = onboardingActionAttempt("agent_sign_in", agentId);
								void verify([agentId]).then(() => { if (attempt !== undefined) settleSignIn(agentId, "failed", attempt); });
							}
						}}
					/>
				</div>
			) : null}
			{authError ? <p className="text-xs text-destructive" role="alert">{authError}</p> : null}
			<StepAction>
				{/* At least one ready agent is needed to orchestrate anything; GitHub
				    stays optional (agents still work locally without it). Until one
				    is ready, setup can still be left for later (nothing installs
				    here, or the person would rather do it themselves). */}
				{anyReady ? null : (
					<Button type="button" variant="ghost" onClick={() => setConfirmingSkip(true)}>
						{t("firstRun.skipSetup")}
					</Button>
				)}
				<NextButton onClick={onNext} disabled={!anyReady}>{t("firstRun.continue")}</NextButton>
			</StepAction>
			<ConfirmDialog
				open={confirmingSkip}
				title={t("firstRun.skipConfirmTitle")}
				description={t("firstRun.skipConfirmBody")}
				confirmLabel={t("firstRun.skipSetup")}
				cancelLabel={t("firstRun.skipConfirmCancel")}
				onConfirm={onSkip}
				onOpenChange={setConfirmingSkip}
			/>
		</StepSplit>
	);
}
