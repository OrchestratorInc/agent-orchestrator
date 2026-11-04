import type { ProjectSource } from "@aoagents/product-ui";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { Cloud, FolderClosed, GitFork, LoaderCircle, RefreshCw, Smartphone, type LucideIcon } from "lucide-react";
import { animate, AnimatePresence, motion, useIsPresent, useMotionValue, useReducedMotion } from "motion/react";
import {
	createContext,
	useCallback,
	useContext,
	useEffect,
	useLayoutEffect,
	useMemo,
	useRef,
	useState,
	type ReactNode,
} from "react";
import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";
import type { components } from "../../../api/schema";
import { useAgentAuthPlans, useStartAgentAuth } from "../../hooks/useAgentAuth";
import {
	cacheAgentReadiness,
	ensureAgentReadiness,
	useAgentReadinessQuery,
	type AgentReadinessSnapshot,
} from "../../hooks/useAgentReadinessQuery";
import { useCloudGate } from "../../hooks/useCloudGate";
import { useCloudLocalAuth } from "../../hooks/useCloudLocalAuth";
import { useMigrationOffer } from "../../hooks/useMigrationOffer";
import {
	systemRequirementsQueryKey,
	useGitHubAuthRequirement,
	useSystemRequirementsGate,
} from "../../hooks/useSystemRequirementsGate";
import { useWorkspaceQuery } from "../../hooks/useWorkspaceQuery";
import type { AgentId } from "../../lib/agent-options";
import { DEFAULT_AGENT_PRIORITY_RANK } from "../../lib/agent-select-options";
import { apiClient, apiErrorMessage } from "../../lib/api-client";
import { aoBridge } from "../../lib/bridge";
import { writeChatComposerText } from "../../lib/chat-drafts";
import { useCloudSession } from "../../lib/cloud-session";
import { sessionNavigateTarget } from "../../lib/navigate-to-session";
import { useOnboardingStep } from "../../lib/onboarding-telemetry";
import { usesPreviewWorkspaceData } from "../../lib/preview-mode";
import { useShell } from "../../lib/shell-context";
import { cn } from "../../lib/utils";
import { useLocalSignInDialogStore } from "../../stores/local-signin-dialog-store";
import { useUiStore } from "../../stores/ui-store";
import { CLOUD_PROJECT_KIND, findProjectOrchestrator, STANDALONE_PROJECT_KIND } from "../../types/workspace";
import { AgentAvatar } from "../AgentAvatar";
import { CloudLocalSignInDialog } from "../CloudLocalSignInDialog";
import { ConfirmDialog } from "../ConfirmDialog";
import { CreateProjectFlow, GitHubIcon } from "../CreateProjectFlow";
import { GitHubOnboardingNotice } from "../GitHubOnboardingNotice";
import { dragStyle } from "../PageTopbar";
import { closeAuthTerminal, HarnessAuthTerminalPanel, type AuthTerminalWorkflow } from "../settings/harness/HarnessAuthTerminalPanel";
import appIcon from "../../assets/app-icon.png";
import { Button } from "../ui/button";
import { Label } from "../ui/label";
import {
	PreviewBoard,
	PreviewChat,
	PreviewNewChat,
	PreviewScene,
	PreviewSidebar,
	PreviewStage,
	type PreviewCard,
	type PreviewSidebarProject,
	type ScenePage,
} from "./OnboardingPreviews";
import { PhoneWorkersScreen } from "./OnboardingPhone";

// Four pages: what Orchestrator is (with its three-part story playing in the
// preview), the optional account, the tools a worker needs (GitHub CLI and a
// coding agent), and the first project, which ends with the user's first
// message to its orchestrator. Log in and Your tools are skipped when there is
// nothing to do on them.
const STEPS = ["welcome", "signin", "tools", "project"] as const;
type Step = (typeof STEPS)[number];
// Where a step stored by the earlier seven-page flow resumes.
const LEGACY_STEPS: Record<string, Step> = {
	github: "tools",
	agents: "tools",
	orchestrator: "project",
	start: "project",
};
// Resume on the same page after a relaunch mid-setup (per-viewer convenience).
const STEP_STORAGE_KEY = "ao.firstRun.step";
// Always listed so a machine with no agents still has something to install.
const SUGGESTED_AGENTS = ["claude-code", "codex"];
// Agents the daemon can install in one click (POST /api/v1/system/install/{target}).
const AGENT_INSTALL_TARGET: Partial<Record<string, SystemInstallTarget>> = {
	"claude-code": "claude",
	codex: "codex",
	opencode: "opencode",
	"opencode-v2": "opencode-v2",
	copilot: "copilot",
};
const READINESS_POLL_MS = 3_000;
const INSTALL_POLL_MS = 1_000;
// --duration-normal and --ease-out, for motion's JS-driven transitions.
const FADE = { duration: 0.15, ease: [0.23, 1, 0.32, 1] } as const;
/** The copy column's width beside the stage. */
const COPY_WIDTH = 520;
const COLLAPSE_SPRING = { type: "spring", visualDuration: 0.34, bounce: 0.04 } as const;
/** The stage opening or closing (the copy column's width). */
const COLLAPSE_MS = 340;
const COLLAPSE_EASE = "cubic-bezier(0.32, 0.72, 0, 1)";
/** The copy's left padding (pl-12). */
const COPY_INSET_LEFT = 48;
/** Pages that lead with the app icon. */
const STEPS_WITH_ICON: ReadonlySet<Step> = new Set<Step>(["welcome", "signin"]);
/** The icon (48px) and the gap under it (20px). */
const ICON_SLOT_HEIGHT = 68;
const IconSlotContext = createContext(false);
/** The row's width when the page being left was centred across it (else 0),
 * so a left-aligned page arriving from it starts centred and slides over. */
const PreviousCenteredContext = createContext(0);

const STARTER_PROMPTS = [
	{ label: "firstRun.startPromptImproveLabel", prompt: "firstRun.startPromptImprove" },
	{ label: "firstRun.startPromptBugLabel", prompt: "firstRun.startPromptBug" },
	{ label: "firstRun.startPromptTestsLabel", prompt: "firstRun.startPromptTests" },
] as const;

/** "How Orchestrator works": the three parts the welcome preview plays. */
const ORCH_POINTS = [
	{ title: "firstRun.orchPoint1Title", body: "firstRun.orchPoint1Body" },
	{ title: "firstRun.orchPoint2Title", body: "firstRun.orchPoint2Body" },
	{ title: "firstRun.orchPoint3Title", body: "firstRun.orchPoint3Body" },
] as const;
/** How long each part of the story holds before the next plays. */
const POINT_HOLD_MS = [4200, 3800, 5200] as const;
/** The order the last point settles the example work (task indexes), and the
 * gap between moves. The one waiting on you settles last, in place. */
const SORT_ORDER = [3, 2, 0, 1] as const;
const SORT_BEAT_MS = 520;

type SystemInstallTarget = "gh" | "claude" | "codex" | "opencode" | "opencode-v2" | "copilot";
type InstallJob = components["schemas"]["InstallJob"];

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
	useEffect(() => {
		if (replayRequest === handledReplay.current) return;
		handledReplay.current = replayRequest;
		storeStep(null);
		setOpen(true);
	}, [replayRequest]);

	if (!open) return null;
	return (
		<OnboardingFlow
			onFinish={() => {
				setOpen(false);
				markSeen();
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
	const [footerSlot, setFooterSlot] = useState<HTMLDivElement | null>(null);
	const [confirmingSkip, setConfirmingSkip] = useState(false);
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
	const stageCollapsed = step === "project";
	// Back from the project page: the stage opens first, then the phone slides
	// in, so the two don't play over each other.
	// Whether the page being left showed the app icon, so the next page's
	// icon slot starts from it and collapses (or grows) instead of cutting.
	const lastStep = useRef(step);
	const previousHadIcon = STEPS_WITH_ICON.has(lastStep.current);
	const previousCentered = wide && lastStep.current === "project" && step !== "project";
	// Whether the phone was out when the project page opened (it came from Log
	// in), so it stays there while the project page is up.
	const [phoneWasShown, setPhoneWasShown] = useState(step === "signin");
	useEffect(() => {
		if (step !== "project") setPhoneWasShown(step === "signin");
	}, [step]);
	useEffect(() => {
		lastStep.current = step;
	}, [step]);
	// Back from the project page: the stage opens first, then the phone slides
	// in, so the two don't play over each other.
	const phoneDelay = lastStep.current === "project" && step !== "project" ? COLLAPSE_MS / 1000 : 0;
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
	const goNext = () => {
		for (let i = index + 1; i < STEPS.length; i++) {
			const next = STEPS[i]!;
			if (i === STEPS.length - 1 || !skips(next)) {
				setStep(next);
				return;
			}
		}
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
	const first = step === "welcome";
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
	// The last point sorts the board one card at a time, a beat apart, so each
	// move reads on its own instead of all cards crossing at once. The beat is
	// keyed to the loop and point it belongs to, so a new run starts from 0 on
	// its first frame instead of flashing the previous run's finished board.
	const sorting = step === "welcome" && point === ORCH_POINTS.length - 1;
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
	const toolAgents = useToolAgents();
	const account = cloud.status === "authenticated" ? cloud.session : null;
	const scene = (
		<OnboardingScene
			step={step}
			point={point}
			cycle={cycle}
			sortBeat={sortBeat}
			account={account ? { name: account.user.displayName?.trim() || account.user.email, avatarUrl: account.user.avatarUrl } : null}
			pickerAgents={toolAgents.pickerAgents}
			selectedAgentId={toolAgents.selectedId}
		/>
	);

	return (
		<FooterSlotContext.Provider value={footerSlot}>
		<div
			className={cn("fixed inset-0 z-[calc(var(--z-overlay)-1)] flex flex-col bg-background text-foreground", PRESS_CLASS)}
			data-testid="first-run-onboarding"
			role="dialog"
			aria-modal="true"
			aria-label={t("firstRun.ariaLabel")}
		>
			{/* Window drag strip over the top edge; the macOS traffic lights sit in it. */}
			<div aria-hidden="true" className="absolute inset-x-0 top-0 z-10 h-toolbar" style={dragStyle} />
			{/* The copy changes per step; the preview stage on the right stays
			    mounted with one app window whose parts animate from step to step
			    (OnboardingScene), so nothing in it is ever swapped out whole. */}
			{/* On the project step the copy's column widens over the whole window
			    (the stage column closes to nothing) so the setup sits centred; the
			    stage keeps its full layout width meanwhile and is cropped, not
			    rescaled. */}
			<div ref={rowRef} className="flex min-h-0 flex-1 overflow-hidden">
				{/* A CSS transition, not a JS spring: it always lands, even if frames
				    are dropped while the next page mounts. */}
				<div
					className="relative min-h-0 shrink-0"
					style={{
						width: !wide ? "100%" : stageCollapsed ? rowWidth : COPY_WIDTH,
						transition: reduceMotion ? undefined : `width ${COLLAPSE_MS}ms ${COLLAPSE_EASE}`,
					}}
				>
					<IconSlotContext.Provider value={previousHadIcon}>
					<PreviousCenteredContext.Provider value={previousCentered ? rowWidth : 0}>
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
							{step === "welcome" ? <WelcomeStep point={point} cycle={cycle} onNext={goNext} /> : null}
							{step === "signin" ? <SignInStep onNext={goNext} /> : null}
							{step === "tools" ? <ToolsStep onNext={goNext} /> : null}
							{step === "project" ? <FirstProjectStep onFinish={onFinish} /> : null}
						</motion.div>
					</AnimatePresence>
					</PreviousCenteredContext.Provider>
					</IconSlotContext.Provider>
				</div>
				{wide ? (
					<div className="flex min-h-0 min-w-0 flex-1">
						{/* Beside the phone, the window drops transparency so both devices show
						    the theme in the same colours. */}
						<PreviewStage
							frameWidth={rowWidth - COPY_WIDTH}
							phoneDelay={phoneDelay}
							phone={<ExamplePhone />}
							// The phone stays put into the project page (the stage closes over
							// it), so going back doesn't slide it in a second time.
							phoneShown={step === "signin" || (step === "project" && phoneWasShown)}
							opaque={step === "signin" || (step === "project" && phoneWasShown)}
						>
							{scene}
						</PreviewStage>
					</div>
				) : null}
			</div>
			<div className="flex h-14 shrink-0 items-center justify-between gap-3 border-t border-border pl-12 pr-4">
				<Progress index={index} />
				<div className="flex items-center gap-2">
					{/* Only from the project page on: the earlier pages are quick, and
					    skipping there would leave the app without tools. Kept in place
					    (invisible) before then so nothing shifts when it appears. */}
					<Button type="button" variant="ghost" className={cn("text-muted-foreground", step !== "project" && "invisible")} onClick={() => setConfirmingSkip(true)}>
						{t("firstRun.skipSetup")}
					</Button>
					<Button type="button" variant="ghost" className={cn(first && "invisible")} onClick={goBack}>
						{t("firstRun.back")}
					</Button>
					{/* Steps portal their one primary action here, outside the fade. */}
					<div ref={setFooterSlot} className="contents" />
				</div>
			</div>
			<ConfirmDialog
				open={confirmingSkip}
				title={t("firstRun.skipConfirmTitle")}
				description={t("firstRun.skipConfirmBody")}
				confirmLabel={t("firstRun.skipSetup")}
				cancelLabel={t("firstRun.skipConfirmCancel")}
				onConfirm={() => {
					setConfirmingSkip(false);
					onFinish();
				}}
				onOpenChange={setConfirmingSkip}
			/>
		</div>
		</FooterSlotContext.Provider>
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

const FooterSlotContext = createContext<HTMLDivElement | null>(null);
/** Portals the step's own action into the bottom strip, which does not fade. */
function StepAction({ children }: { children: ReactNode }) {
	const slot = useContext(FooterSlotContext);
	// The outgoing step stays mounted while it fades; only the current one owns the strip.
	const present = useIsPresent();
	if (!slot || !present) return null;
	return createPortal(children, slot);
}

/** One page's copy and controls. The app preview beside it is the flow's one
 * persistent scene (OnboardingScene), driven by the step. */
function StepSplit({
	icon,
	title,
	body,
	children,
	centered = false,
}: {
	/** Lead with the app icon. */
	icon?: boolean;
	/** The stage is closed on this step: centre the copy across the window. */
	centered?: boolean;
	title: string;
	body?: ReactNode;
	children?: ReactNode;
}) {
	// Arriving from the centred project page, a left-aligned page starts where
	// the centred copy was and slides left as the column narrows, mirroring
	// how the copy drifts to the middle on the way there.
	const fromCenteredRow = useContext(PreviousCenteredContext);
	const reduceMotion = useReducedMotion();
	const contentRef = useRef<HTMLDivElement>(null);
	const x = useMotionValue(0);
	// Once, on arrival, before the first paint.
	const [arrivedFrom] = useState(fromCenteredRow);
	useLayoutEffect(() => {
		if (centered || !arrivedFrom || reduceMotion) return;
		const width = contentRef.current?.offsetWidth ?? 0;
		x.set(Math.max(0, (arrivedFrom - width) / 2 - COPY_INSET_LEFT));
		const slide = animate(x, 0, { duration: COLLAPSE_MS / 1000, ease: [0.32, 0.72, 0, 1] });
		return () => slide.stop();
	}, [arrivedFrom, centered, reduceMotion, x]);
	return (
		// Fixed top anchor: the title sits at the same height on every step,
		// centred or not, so only what is under it changes and the copy only
		// moves sideways as the stage closes (the column widens under it).
		<div
			className={cn(
				"flex min-h-0 w-full flex-col overflow-y-auto bg-background pb-10 pt-[max(5rem,18vh)]",
				centered ? "items-center px-12" : "pl-12 pr-10",
			)}
		>
			<motion.div ref={contentRef} className="flex w-full max-w-md flex-col" style={{ x }}>
				<IconSlot shown={Boolean(icon)} />
				<h1 className="text-balance text-heading-lg font-semibold leading-tight tracking-tight text-foreground">{title}</h1>
				{body ? <p className="max-w-sm pt-3 text-pretty text-sm leading-relaxed text-muted-foreground">{body}</p> : null}
				{children ? <div className="flex flex-col gap-4 pt-6">{children}</div> : null}
			</motion.div>
		</div>
	);
}

/** The app icon over the title. Its height animates from the previous page's
 * (shown or not), so the title glides up or down instead of jumping. */
function IconSlot({ shown }: { shown: boolean }) {
	const previous = useContext(IconSlotContext);
	const reduceMotion = useReducedMotion();
	const [mountedWith] = useState(previous);
	if (!shown && !mountedWith) return null;
	const open = { height: ICON_SLOT_HEIGHT, opacity: 1, scale: 1 };
	const closed = { height: 0, opacity: 0, scale: 0.9 };
	return (
		<motion.div
			aria-hidden={!shown}
			className="origin-top-left overflow-hidden"
			initial={reduceMotion ? false : mountedWith ? open : closed}
			animate={shown ? open : closed}
			// Leaving is quick (it shouldn't linger over the next title);
			// arriving takes the stage's pace.
			transition={reduceMotion ? { duration: 0 } : shown ? COLLAPSE_SPRING : { duration: 0.16, ease: [0.23, 1, 0.32, 1] }}
		>
			<img src={appIcon} alt="" className="size-12 select-none" draggable={false} />
		</motion.div>
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
function CheckRow({ icon, label, detail, trailing }: { icon?: ReactNode; label: ReactNode; detail?: ReactNode; trailing?: ReactNode }) {
	return (
		<li className="flex items-center gap-3 px-3 py-2 [&+&]:border-t [&+&]:border-border">
			{icon ? <span className="flex shrink-0" aria-hidden="true">{icon}</span> : null}
			<span className="min-w-0 flex-1">
				<span className="block truncate text-sm text-foreground">{label}</span>
				{detail ? <span className="block truncate text-xs text-muted-foreground">{detail}</span> : null}
			</span>
			<span className="flex h-control-md shrink-0 items-center">{trailing}</span>
		</li>
	);
}

/** Example work for the previews: clearly sample tasks, not the user's data. */
function useExampleTasks() {
	const { t } = useTranslation();
	return useMemo(() => [
		{ id: "example-1", title: t("firstRun.exampleTask1"), provider: "claude-code", branch: "tests/sign-in" },
		{ id: "example-2", title: t("firstRun.exampleTask2"), provider: "codex", branch: "fix/auth-redirect" },
		{ id: "example-3", title: t("firstRun.exampleTask3"), provider: "claude-code", branch: "docs/readme" },
		{ id: "example-4", title: t("firstRun.exampleTask4"), provider: "codex", branch: "perf/search" },
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
 * The flow's one app window. Its state comes from the step (and, on Welcome,
 * the story's current part). It looks exactly like the app; only the changes
 * are eased: the center panel swaps pages with a short blurred cross-fade,
 * session rows unfold in the sidebar, cards settle onto the board and glide
 * between lanes, the agent picker opens like the app's menus, and the account
 * row rolls over.
 */
function OnboardingScene({
	step,
	point,
	cycle,
	sortBeat,
	account,
	pickerAgents,
	selectedAgentId,
}: {
	step: Step;
	point: number;
	cycle: number;
	sortBeat: number;
	account: { name: string; avatarUrl?: string } | null;
	pickerAgents: Array<{ id: string; label: string; dimmed?: boolean }>;
	selectedAgentId?: string;
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
	const sidebarProject: PreviewSidebarProject = {
		name: project,
		active: page === "board",
		orchestratorActive: page === "chat",
		sessions: cards
			.slice()
			.sort((a, b) => tasks.findIndex((task) => task.id === a.id) - tasks.findIndex((task) => task.id === b.id))
			.map((card) => ({ title: card.title, status: card.status, working: card.working, minutesAgo: card.minutesAgo })),
	};
	return (
		<PreviewScene
			page={page}
			sidebar={<PreviewSidebar projects={[sidebarProject]} account={account} />}
			chat={
				<PreviewChat
					runId={cycle}
					title={t("shell.orchestrator")}
					provider="claude-code"
					message={t("firstRun.exampleMessage")}
					reply={t("firstRun.exampleReply")}
				/>
			}
			board={
				<PreviewBoard
					key={boardKey}
					cards={boardCards}
				/>
			}
			newChat={
				<PreviewNewChat
					agents={pickerAgents}
					selectedId={selectedAgentId}
					project={project}
					menuOpen={page === "new"}
				/>
			}
		/>
	);
}

/** The same example work on the mobile app's Workers tab, as the story ends. */
function ExamplePhone() {
	const { t } = useTranslation();
	const [task1, task2, task3, task4] = useExampleTasks();
	return (
		<PhoneWorkersScreen
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

function WelcomeStep({ point, cycle, onNext }: { point: number; cycle: number; onNext: () => void }) {
	const { t } = useTranslation();
	const reduceMotion = useReducedMotion();
	const current = ORCH_POINTS[point]!;
	return (
		<StepSplit
			icon
			title={t("firstRun.welcomeTitle")}
			body={t("firstRun.welcomeBody")}
		>
			{/* Narrates the preview: one caption for the part playing, under a
			    three-segment timer that fills while it plays. */}
			<div className="flex flex-col gap-3 pt-6" aria-hidden="true">
				<div className="flex gap-1">
					{ORCH_POINTS.map(({ title }, i) => (
						<span key={title} className="h-0.5 w-8 overflow-hidden rounded-full bg-muted">
							{i < point ? <span className="block h-full w-full bg-foreground/60" /> : null}
							{i === point ? (
								<motion.span
									key={`${cycle}-${point}`}
									className="block h-full origin-left bg-foreground/60"
									initial={{ scaleX: reduceMotion ? 1 : 0 }}
									animate={{ scaleX: 1 }}
									transition={reduceMotion ? { duration: 0 } : { duration: POINT_HOLD_MS[point] / 1000, ease: "linear" }}
								/>
							) : null}
						</span>
					))}
				</div>
				<div className="grid">
					<AnimatePresence initial={false}>
						<motion.div
							key={point}
							className="col-start-1 row-start-1 flex flex-col gap-1"
							initial={reduceMotion ? false : { opacity: 0, y: 8 }}
							animate={{ opacity: 1, y: 0 }}
							exit={reduceMotion ? { opacity: 0, transition: { duration: 0 } } : { opacity: 0, y: -8 }}
							transition={{ duration: 0.3, ease: [0.23, 1, 0.32, 1] }}
						>
							<span className="text-sm font-medium text-foreground">{t(current.title)}</span>
							<span className="max-w-sm text-pretty text-sm leading-relaxed text-muted-foreground">{t(current.body)}</span>
						</motion.div>
					</AnimatePresence>
				</div>
			</div>
			{/* The whole story for assistive tech, which skips the animation. */}
			<ol className="sr-only">
				{ORCH_POINTS.map(({ title, body }) => (
					<li key={title}>{t(title)}: {t(body)}</li>
				))}
			</ol>
			<StepAction>
				<Button variant="primary" onClick={onNext} autoFocus>{t("firstRun.getStarted")}</Button>
			</StepAction>
		</StepSplit>
	);
}

// ── 1. Sign in (optional) ───────────────────────────────────────────────────

/** Why an account is worth it: what only an account unlocks. */
const SIGN_IN_PERKS: Array<{ icon: LucideIcon; title: string; detail: string }> = [
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
			icon
			title={t("firstRun.signInTitle")}
		>
			{cloud.status === "loading" ? (
				<Waiting>{t("firstRun.signInChecking")}</Waiting>
			) : account ? (
				<ul className="overflow-hidden rounded-lg bg-card">
					<CheckRow
						label={accountName}
						detail={account.user.displayName ? account.user.email : undefined}
						trailing={<RowState>{t("firstRun.signedIn")}</RowState>}
					/>
				</ul>
			) : (
				<>
				<ul className="overflow-hidden rounded-lg bg-card">
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
			{/* The page's actions live in the bottom bar: Continue once logged in,
			    else Do later beside Log in. */}
			<StepAction>
				{account ? (
					<Button variant="primary" onClick={onNext}>{t("firstRun.continue")}</Button>
				) : cloud.status === "loading" ? null : (
					<>
						<Button variant="ghost" onClick={onNext}>
							{t("firstRun.doLater")}
						</Button>
						<Button variant="primary" onClick={signIn} disabled={browserWaiting}>
							{browserWaiting ? <LoaderCircle className="size-icon-base animate-spin" aria-hidden="true" /> : null}
							{t("firstRun.signInBrowser")}
						</Button>
					</>
				)}
			</StepAction>
		</StepSplit>
	);
}

// ── 2. Your tools: GitHub CLI and coding agents ─────────────────────────────

/** Starts and polls a one-click system install (gh or an agent CLI). */
function useSystemInstall(onSucceeded: (target: SystemInstallTarget) => void) {
	const [jobs, setJobs] = useState<Partial<Record<SystemInstallTarget, InstallJob>>>({});
	const [errors, setErrors] = useState<Partial<Record<SystemInstallTarget, string>>>({});
	const timers = useRef(new Map<SystemInstallTarget, number>());
	const onSucceededRef = useRef(onSucceeded);
	onSucceededRef.current = onSucceeded;
	useEffect(() => () => {
		for (const timer of timers.current.values()) window.clearInterval(timer);
	}, []);
	const active = (job?: InstallJob) => Boolean(job && ["running", "waiting_for_idle", "installing", "verifying"].includes(job.status));
	const start = useCallback(async (target: SystemInstallTarget) => {
		setErrors((current) => ({ ...current, [target]: undefined }));
		const { data, error } = await apiClient.POST("/api/v1/system/install/{target}", { params: { path: { target } } });
		if (error || !data) {
			setErrors((current) => ({ ...current, [target]: apiErrorMessage(error, "Could not start the install.") }));
			return;
		}
		setJobs((current) => ({ ...current, [target]: data }));
		if (data.status === "succeeded") onSucceededRef.current(target);
		if (!active(data)) return;
		const timer = window.setInterval(() => {
			void (async () => {
				const next = await apiClient.GET("/api/v1/system/install/{target}", { params: { path: { target } } });
				if (next.error || !next.data) return;
				setJobs((current) => ({ ...current, [target]: next.data }));
				if (active(next.data)) return;
				window.clearInterval(timer);
				timers.current.delete(target);
				if (next.data.status === "succeeded") onSucceededRef.current(target);
				else setErrors((current) => ({ ...current, [target]: next.data.error || "The install did not finish." }));
			})();
		}, INSTALL_POLL_MS);
		timers.current.set(target, timer);
	}, []);
	return { start, isRunning: (target: SystemInstallTarget) => active(jobs[target]), error: (target: SystemInstallTarget) => errors[target] };
}

type AgentRowState = "ready" | "signin" | "missing" | "checking";

function agentRowState(agent: AgentReadinessSnapshot): AgentRowState {
	if (agent.effectiveReadiness === "ready") return "ready";
	if (agent.installation.state === "not_installed") return "missing";
	if (agent.authentication.state === "unauthorized") return "signin";
	return "checking";
}

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
	const install = useSystemInstall((target) => {
		if (target === "gh") {
			void queryClient.invalidateQueries({ queryKey: systemRequirementsQueryKey });
			void ghAuth.refetch();
		} else void refresh();
	});

	const gh = gate.requirements.find((requirement) => requirement.id === "gh");
	const ghInstalled = gh?.satisfied === true;
	const ghSignedIn = ghAuth.data?.satisfied === true;
	const ghChecking = gate.checking || ghAuth.isPending;
	const ghInstalling = install.isRunning("gh");

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
				<div className="flex flex-col gap-2">
					<ul className="overflow-hidden rounded-lg bg-card">
						<CheckRow
							icon={<GitHubIcon className="size-icon-base text-muted-foreground" />}
							label={t("firstRun.githubTitle")}
							detail={install.error("gh") ?? (ghInstalled && !ghSignedIn ? t("firstRun.ghSignInHint") : undefined)}
							trailing={ghInstalling ? <RowSpinner />
								: !ghInstalled ? (
									<Button size="sm" variant="secondary" onClick={() => void install.start("gh")}>{t("firstRun.install")}</Button>
								)
								: ghSignedIn ? <RowState>{t("firstRun.signedIn")}</RowState>
								: null}
						/>
						{agents.map(({ agent, state }) => {
							const target = AGENT_INSTALL_TARGET[agent.id];
							const installing = target ? install.isRunning(target) : false;
							const busy = installing || (signingInAgent === agent.id && !signingInReady);
							return (
								<CheckRow
									key={agent.id}
									icon={<AgentAvatar provider={agent.id} className="size-icon-base" decorative />}
									label={agent.label}
									detail={target ? install.error(target) : undefined}
									trailing={busy ? <RowSpinner />
										: state === "ready" ? <RowState>{t("settings.harness.installed")}</RowState>
										: state === "missing" && target ? (
											<Button size="sm" variant="secondary" onClick={() => void install.start(target)}>{t("firstRun.install")}</Button>
										) : state === "missing" ? <RowState>{t("firstRun.agentMissing")}</RowState>
										: state === "signin" ? (
											<Button size="sm" variant="secondary" disabled={startAuth.isPending} onClick={() => void signIn(agent.id)}>{t("firstRun.signIn")}</Button>
										) : null}
								/>
							);
						})}
					</ul>
					{/* The existing login flow: an embedded `gh auth login` terminal. */}
					{ghInstalled && !ghSignedIn ? <GitHubOnboardingNotice embedded /> : null}
					{anyReady ? null : <p className="text-xs text-muted-foreground">{t("firstRun.agentsBodyNone")}</p>}
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
				<Button variant="primary" onClick={onNext} disabled={!anyReady}>{t("firstRun.continue")}</Button>
			</StepAction>
		</StepSplit>
	);
}

// ── 3. Your first project, then its first task ──────────────────────────────

// Same tile as Add Project's source step (CreateProjectFlow), so this page
// reads as the start of that flow.
function SourceTile({ icon, label, hint, onClick }: { icon: ReactNode; label: string; hint: string; onClick: () => void }) {
	return (
		<button
			type="button"
			className="group flex items-center gap-3 rounded-lg bg-interactive-hover px-4 py-3 text-left hover:bg-interactive-active disabled:pointer-events-none disabled:opacity-50"
			aria-label={label}
			onClick={onClick}
		>
			<span className="flex shrink-0 text-muted-foreground group-hover:text-foreground">{icon}</span>
			<span className="min-w-0">
				<span className="block text-sm font-medium text-foreground">{label}</span>
				<span className="mt-1 block text-pretty text-xs leading-row text-muted-foreground">{hint}</span>
			</span>
		</button>
	);
}

/** The user's own local projects, for the sidebar previews. */
function useLocalProjects() {
	return (useWorkspaceQuery().data ?? []).filter(
		(candidate) => candidate.kind !== STANDALONE_PROJECT_KIND && candidate.kind !== CLOUD_PROJECT_KIND,
	);
}

/** One page in two phases: add a project (open or clone a repo), and once it
 * exists the same page asks for the first task and hands it to the project's
 * orchestrator. The stage is closed here, so both phases sit centred. */
function FirstProjectStep({ onFinish }: { onFinish: () => void }) {
	const project = useLocalProjects()[0];
	return project ? <FirstTask project={project} onFinish={onFinish} /> : <ProjectChooser />;
}

/** The ready agent we integrate best with: Claude Code, then Codex, then the
 * default priority order (Cursor, OpenCode, OpenCode 2, Aider), then any other
 * ready agent. Undefined until one is ready, which leaves the usual agent
 * questions in place. */
function useBestReadyAgent(): string | undefined {
	const readiness = useAgentReadinessQuery();
	return useMemo(() => {
		const ready = (readiness.data?.agents ?? []).filter((agent) => agent.effectiveReadiness === "ready").map((agent) => agent.id as string);
		const rank = (id: string) => DEFAULT_AGENT_PRIORITY_RANK.get(id) ?? Number.MAX_SAFE_INTEGER;
		return ready.sort((a, b) => rank(a) - rank(b))[0];
	}, [readiness.data]);
}

function ProjectChooser() {
	const { t } = useTranslation();
	const { cloneProject, createProject, initializeProjectRepository } = useShell();
	const projects = useWorkspaceQuery().data ?? [];
	const [sourceSignal, setSourceSignal] = useState<{ source: ProjectSource; nonce: number } | null>(null);
	const request = (source: ProjectSource) => setSourceSignal({ source, nonce: Date.now() });
	const creating = useUiStore((state) => state.projectCreationPending);
	// No agent questions on the first project: the best ready agent runs both
	// the workers and the orchestrator.
	const agent = useBestReadyAgent();

	return (
		<StepSplit
			centered
			title={t("firstRun.projectTitle")}
			body={t("firstRun.projectBody")}
		>
			{/* Adding a project moves the page on by itself; Continue holds the strip steady. */}
			<StepAction>
				<Button variant="primary" disabled>{t("firstRun.continue")}</Button>
			</StepAction>
			<div className="flex flex-col gap-2">
				<SourceTile
					icon={<FolderClosed className="size-5" aria-hidden="true" strokeWidth={1.75} />}
					label={t("createProject.openLocal")}
					hint={t("createProject.openLocalDesc")}
					onClick={() => request("local")}
				/>
				<SourceTile
					icon={<GitFork className="size-5" aria-hidden="true" strokeWidth={1.75} />}
					label={t("createProject.cloneFromGit")}
					hint={t("createProject.cloneFromGitDesc")}
					onClick={() => request("clone")}
				/>
			</div>
			<CreateProjectFlow
				existingProjectNames={projects.map((project) => project.name)}
				existingProjectPaths={projects.map((project) => project.path)}
				mode="choose"
				onCloneProject={cloneProject}
				// The new project turns this page into the first-task composer.
				onCreateProject={createProject}
				onInitializeProject={initializeProjectRepository}
				sourceSignal={sourceSignal}
				autoAgent={agent}
			/>
			{creating ? <Waiting>{t("firstRun.projectCreating")}</Waiting> : null}
		</StepSplit>
	);
}

function FirstTask({ project, onFinish }: { project: ReturnType<typeof useLocalProjects>[number]; onFinish: () => void }) {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const workspaces = useWorkspaceQuery().data ?? [];
	const orchestrator = findProjectOrchestrator(workspaces, project.id);
	const provisioning = useUiStore((state) => state.provisioningProjectIds.has(project.id));
	const completeTask = useOnboardingStep("first_task", true);
	const [prompt, setPrompt] = useState(() => t(STARTER_PROMPTS[0].prompt));

	// Sends the first message to the orchestrator itself, then opens its chat
	// so the person watches it start. If the orchestrator is still starting,
	// the send waits for it; if sending fails, the text lands in its composer.
	const [sending, setSending] = useState(false);
	const sentRef = useRef(false);
	const deliver = useCallback(async (session: NonNullable<typeof orchestrator>, message: string) => {
		if (sentRef.current) return;
		sentRef.current = true;
		const { error } = await apiClient
			.POST("/api/v1/sessions/{sessionId}/send", { params: { path: { sessionId: session.id } }, body: { message, userAuthored: true } })
			.catch((thrown: unknown) => ({ error: thrown }));
		// The live chat scopes its draft by session id + createdAt (ChatWorkspace).
		if (error) writeChatComposerText({ sessionId: session.id, incarnation: session.createdAt ?? session.id }, message);
		completeTask();
		onFinish();
		void navigate(sessionNavigateTarget(project.id, session.id));
	}, [completeTask, navigate, onFinish, project.id]);
	const send = () => {
		setSending(true);
		if (orchestrator) void deliver(orchestrator, prompt.trim());
	};
	useEffect(() => {
		if (sending && orchestrator) void deliver(orchestrator, prompt.trim());
	}, [sending, orchestrator, deliver, prompt]);

	return (
		<StepSplit
			centered
			title={t("firstRun.startTitle")}
			body={t("firstRun.startBody", { project: project.name })}
		>
			<div className="flex flex-col gap-2">
				<Label htmlFor="first-run-prompt">{t("firstRun.startPromptLabel")}</Label>
				{/* No shared textarea primitive yet: Input's chrome at the textarea minimum height. */}
				<textarea
					id="first-run-prompt"
					className="min-h-(--size-textarea-min) w-full min-w-0 resize-none rounded-md border border-transparent bg-input/50 px-3 py-2 text-sm text-foreground transition-[color,box-shadow,background-color] outline-none placeholder:text-muted-foreground focus-visible:outline-none"
					value={prompt}
					onChange={(event) => setPrompt(event.target.value)}
				/>
				<div className="flex flex-wrap gap-1.5">
					{STARTER_PROMPTS.map((starter) => (
						<button
							key={starter.prompt}
							className={cn(
								"rounded-full border border-border px-2.5 py-1 text-xs text-muted-foreground hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-focus focus-visible:ring-ring/60",
								prompt === t(starter.prompt) && "bg-accent text-foreground",
							)}
							onClick={() => setPrompt(t(starter.prompt))}
							type="button"
						>
							{t(starter.label)}
						</button>
					))}
				</div>
			</div>
			{provisioning || !orchestrator ? <Waiting>{t("firstRun.settingUp")}</Waiting> : null}
			<StepAction>
				<Button variant="primary" onClick={send} disabled={!prompt.trim() || sending}>
					{sending ? <LoaderCircle className="size-icon-base animate-spin" aria-hidden="true" /> : null}
					{t("firstRun.openOrchestrator")}
				</Button>
			</StepAction>
		</StepSplit>
	);
}
