import type { ProjectSource } from "@aoagents/product-ui";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { Cloud, FolderClosed, GitFork, LoaderCircle, RefreshCw, Smartphone, type LucideIcon } from "lucide-react";
import { AnimatePresence, motion, useIsPresent, useReducedMotion } from "motion/react";
import {
	createContext,
	useCallback,
	useContext,
	useEffect,
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
import { CreateProjectFlow } from "../CreateProjectFlow";
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
	PreviewSidebar,
	PreviewStage,
	PreviewWindow,
	type PreviewCard,
	type PreviewSidebarProject,
	type PreviewSidebarSession,
} from "./OnboardingPreviews";
import { PhoneWorkersScreen } from "./OnboardingPhone";

// Setup first (account, GitHub CLI, agents), then a three-page tutorial that
// ends with the user's first message to their project's orchestrator.
const STEPS = ["welcome", "signin", "github", "agents", "orchestrator", "project", "start"] as const;
type Step = (typeof STEPS)[number];
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

const STARTER_PROMPTS = [
	{ label: "firstRun.startPromptImproveLabel", prompt: "firstRun.startPromptImprove" },
	{ label: "firstRun.startPromptBugLabel", prompt: "firstRun.startPromptBug" },
	{ label: "firstRun.startPromptTestsLabel", prompt: "firstRun.startPromptTests" },
] as const;

const ORCH_POINTS = ["firstRun.orchPoint1", "firstRun.orchPoint2", "firstRun.orchPoint3"] as const;

type SystemInstallTarget = "gh" | "claude" | "codex" | "opencode" | "opencode-v2" | "copilot";
type InstallJob = components["schemas"]["InstallJob"];

function readStoredStep(): Step {
	try {
		const stored = window.localStorage.getItem(STEP_STORAGE_KEY);
		return STEPS.includes(stored as Step) ? (stored as Step) : "welcome";
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
	// Which point of "How Orchestrator works" is showing.
	const [point, setPoint] = useState(0);
	const setStep = (next: Step) => {
		storeStep(next);
		setStepState(next);
	};
	const index = STEPS.indexOf(step);
	const reduceMotion = useReducedMotion();
	const [footerSlot, setFooterSlot] = useState<HTMLDivElement | null>(null);
	const [previewSlot, setPreviewSlot] = useState<HTMLDivElement | null>(null);
	const goNext = () => {
		const next = STEPS[Math.min(index + 1, STEPS.length - 1)]!;
		if (next === "orchestrator") setPoint(0);
		setStep(next);
	};
	const goBack = () => {
		if (step === "orchestrator" && point > 0) {
			setPoint(point - 1);
			return;
		}
		const previous = STEPS[Math.max(index - 1, 0)]!;
		if (previous === "orchestrator") setPoint(ORCH_POINTS.length - 1);
		setStep(previous);
	};
	useOnboardingStep("welcome", step === "welcome");
	const first = step === "welcome";

	return (
		<FooterSlotContext.Provider value={footerSlot}>
		<PreviewSlotContext.Provider value={previewSlot}>
		<div
			className={cn("fixed inset-0 z-[calc(var(--z-overlay)-1)] flex flex-col bg-background text-foreground", PRESS_CLASS)}
			data-testid="first-run-onboarding"
			role="dialog"
			aria-modal="true"
			aria-label={t("firstRun.ariaLabel")}
		>
			{/* Window drag strip over the top edge; the macOS traffic lights sit in it. */}
			<div aria-hidden="true" className="absolute inset-x-0 top-0 z-10 h-toolbar" style={dragStyle} />
			{/* The copy cross-fades per step; the preview stage on the right stays
			    mounted, so the wallpaper, window and phone never blink. Each step
			    portals its window into the stage, and the outgoing step stays
			    mounted under the incoming one until its fade ends. */}
			<div className="grid min-h-0 flex-1 grid-cols-1 min-[900px]:grid-cols-[400px_minmax(0,1fr)]">
				<div className="relative min-h-0">
					<AnimatePresence initial={false}>
						<motion.div
							key={step}
							className="absolute inset-0 flex"
							initial={reduceMotion ? false : { opacity: 0 }}
							animate={{ opacity: 1 }}
							// The outgoing copy leaves at once (the column is see-through in a
							// translucent window, so two steps' text must never overlap). Its
							// preview keeps its own fade in the stage, and the step stays
							// mounted until that finishes.
							exit={{ opacity: 0, transition: { duration: 0 } }}
							transition={FADE}
						>
							{step === "welcome" ? <WelcomeStep onNext={goNext} /> : null}
							{step === "signin" ? <SignInStep onNext={goNext} /> : null}
							{step === "github" ? <GitHubStep onNext={goNext} /> : null}
							{step === "agents" ? <AgentsStep onNext={goNext} /> : null}
							{step === "orchestrator" ? <OrchestratorStep point={point} onPoint={setPoint} onNext={goNext} /> : null}
							{step === "project" ? <ProjectStep onDone={goNext} /> : null}
							{step === "start" ? <StartStep onFinish={onFinish} /> : null}
						</motion.div>
					</AnimatePresence>
				</div>
				<div className="hidden min-h-0 min-[900px]:flex">
					{/* Beside the phone, the window drops transparency so both devices show
					    the theme in the same colours. */}
					<PreviewStage phone={<ExamplePhone />} phoneShown={step === "signin"} opaque={step === "signin"}>
						<div ref={setPreviewSlot} className="absolute inset-0" />
					</PreviewStage>
				</div>
			</div>
			<div className="flex h-14 shrink-0 items-center justify-between gap-3 border-t border-border pl-12 pr-4">
				<Progress index={index} />
				<div className="flex items-center gap-2">
					{/* Kept in place on the first page so nothing shifts later. */}
					<Button type="button" variant="ghost" className={cn("text-muted-foreground", first && "invisible")} onClick={onFinish}>
						{t("firstRun.skipSetup")}
					</Button>
					<Button type="button" variant="ghost" className={cn(first && "invisible")} onClick={goBack}>
						{t("firstRun.back")}
					</Button>
					{/* Steps portal their one primary action here, outside the fade. */}
					<div ref={setFooterSlot} className="contents" />
				</div>
			</div>
		</div>
		</PreviewSlotContext.Provider>
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
const PreviewSlotContext = createContext<HTMLDivElement | null>(null);

/** Puts a step's app window contents into the shared stage. The old step's
 * contents fade out as the new one's fade in; the glass plate under both stays. */
function StagePreview({ children }: { children: ReactNode }) {
	const slot = useContext(PreviewSlotContext);
	const reduceMotion = useReducedMotion();
	if (!slot) return null;
	return createPortal(
		<motion.div
			className="absolute inset-0"
			initial={reduceMotion ? false : { opacity: 0 }}
			animate={{ opacity: 1 }}
			exit={{ opacity: 0, transition: reduceMotion ? { duration: 0 } : FADE }}
			transition={FADE}
		>
			{children}
		</motion.div>,
		slot,
	);
}

/** Portals the step's own action into the bottom strip, which does not fade. */
function StepAction({ children }: { children: ReactNode }) {
	const slot = useContext(FooterSlotContext);
	// The outgoing step stays mounted while it fades; only the current one owns the strip.
	const present = useIsPresent();
	if (!slot || !present) return null;
	return createPortal(children, slot);
}

/** One page: the copy and controls on the left, the app's own UI in the stage. */
function StepSplit({
	icon,
	title,
	body,
	children,
	preview,
}: {
	icon?: ReactNode;
	title: string;
	body?: ReactNode;
	children?: ReactNode;
	preview: ReactNode;
}) {
	return (
		<>
			{/* Fixed top anchor: the title sits at the same height on every step;
			    only what is under it changes. */}
			<div className="flex min-h-0 w-full flex-col overflow-y-auto bg-background pb-10 pl-12 pr-10 pt-[max(5rem,18vh)]">
				<div className="flex w-full max-w-md flex-col">
					{icon ? <div className="pb-5">{icon}</div> : null}
					<h1 className="text-balance text-heading-lg font-semibold leading-tight tracking-tight text-foreground">{title}</h1>
					{body ? <p className="max-w-sm pt-3 text-pretty text-sm leading-relaxed text-muted-foreground">{body}</p> : null}
					{children ? <div className="flex flex-col gap-4 pt-6">{children}</div> : null}
				</div>
			</div>
			<StagePreview>{preview}</StagePreview>
		</>
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
function useExampleCards() {
	const { t } = useTranslation();
	return useMemo(() => {
		const tasks = [
			{ id: "example-1", title: t("firstRun.exampleTask1"), branch: "tests/sign-in", provider: "claude-code" },
			{ id: "example-2", title: t("firstRun.exampleTask2"), branch: "fix/auth-redirect", provider: "codex" },
			{ id: "example-3", title: t("firstRun.exampleTask3"), branch: "docs/readme", provider: "claude-code" },
		] as const;
		return tasks;
	}, [t]);
}

function card(task: ReturnType<typeof useExampleCards>[number], rest: Omit<PreviewCard, "id" | "title" | "branch" | "provider">): PreviewCard {
	return { ...task, ...rest };
}

/** The example project in the sidebar with its board open and the example
 * tasks under it, matching ExampleBoard. */
function useExampleSidebarProjects(): PreviewSidebarProject[] {
	const { t } = useTranslation();
	const [task1, task2, task3] = useExampleCards();
	return [{
		name: t("firstRun.projectPlaceholder"),
		active: true,
		sessions: [
			{ title: task1.title, status: "working", working: true, minutesAgo: 1 },
			{ title: task2.title, status: "needs_input", minutesAgo: 4 },
			{ title: task3.title, status: "mergeable", minutesAgo: 12 },
		],
	}];
}

// ── 0. Welcome ──────────────────────────────────────────────────────────────

/** The welcome page's board, reused (dimmed) as context on other steps. */
function ExampleBoard() {
	const [task1, task2, task3] = useExampleCards();
	return (
		<PreviewBoard
			lanes={[
				{ column: "building", cards: [
					card(task1, { status: "working", displayStatus: "Working", working: true, minutesAgo: 1 }),
					card(task2, { status: "needs_input", minutesAgo: 4 }),
				] },
				{ column: "ready", cards: [
					card(task3, { status: "mergeable", displayStatus: "Mergeable", minutesAgo: 12, prs: [{ number: 42, state: "open" }] }),
				] },
			]}
		/>
	);
}

/** The same example work on the mobile app's Workers tab. */
function ExamplePhone() {
	const { t } = useTranslation();
	const [task1, task2, task3] = useExampleCards();
	return (
		<PhoneWorkersScreen
			project={t("firstRun.projectPlaceholder")}
			workers={[
				{ ...task1, status: "working", minutesAgo: 1 },
				{ ...task2, status: "needs_input", minutesAgo: 4 },
				{ ...task3, status: "mergeable", minutesAgo: 12, prNumber: 42 },
			]}
		/>
	);
}

function WelcomeStep({ onNext }: { onNext: () => void }) {
	const { t } = useTranslation();
	const exampleProjects = useExampleSidebarProjects();
	return (
		<StepSplit
			icon={<img src={appIcon} alt="" className="size-12 select-none" draggable={false} />}
			title={t("firstRun.welcomeTitle")}
			body={t("firstRun.welcomeBody")}
			preview={
				<PreviewWindow sidebar={<PreviewSidebar projects={exampleProjects} />}>
					<ExampleBoard />
				</PreviewWindow>
			}
		>
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
	const exampleProjects = useExampleSidebarProjects();
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
			icon={<img src={appIcon} alt="" className="size-12 select-none" draggable={false} />}
			title={t("firstRun.signInTitle")}
			preview={
				<PreviewWindow
					sidebar={
						<PreviewSidebar
							account={account ? { name: accountName, avatarUrl: account.user.avatarUrl } : null}
							projects={exampleProjects}
						/>
					}
				>
					<ExampleBoard />
				</PreviewWindow>
			}
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
				<div className="flex flex-wrap items-center gap-3">
					<Button variant="primary" onClick={signIn} disabled={browserWaiting}>
						{browserWaiting ? <LoaderCircle className="size-icon-base animate-spin" aria-hidden="true" /> : null}
						{t(browserWaiting ? "firstRun.signInWaiting" : "firstRun.signInBrowser")}
					</Button>
					{browserWaiting ? null : (
						<Button variant="ghost" className="text-muted-foreground" onClick={onNext}>
							{t("firstRun.doLater")}
						</Button>
					)}
					{browserWaiting ? (
						<Button variant="ghost" className="text-muted-foreground" onClick={() => cloud.signIn()}>
							{t("firstRun.signInReopen")}
						</Button>
					) : null}
				</div>
				</>
			)}
			{/* The local email/password dialog is only mounted by the cloud gate. */}
			{localAuth.available && !cloudEnabled ? <CloudLocalSignInDialog /> : null}
			{/* Optional: Continue works either way, and leads once signed in. */}
			{account ? (
				<StepAction>
					<Button variant="primary" onClick={onNext}>{t("firstRun.continue")}</Button>
				</StepAction>
			) : null}
		</StepSplit>
	);
}

// ── 2. GitHub CLI ───────────────────────────────────────────────────────────

/** Starts and polls a one-click system install (gh or an agent CLI). */
function useSystemInstall(onSucceeded: () => void) {
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
		if (data.status === "succeeded") onSucceededRef.current();
		if (!active(data)) return;
		const timer = window.setInterval(() => {
			void (async () => {
				const next = await apiClient.GET("/api/v1/system/install/{target}", { params: { path: { target } } });
				if (next.error || !next.data) return;
				setJobs((current) => ({ ...current, [target]: next.data }));
				if (active(next.data)) return;
				window.clearInterval(timer);
				timers.current.delete(target);
				if (next.data.status === "succeeded") onSucceededRef.current();
				else setErrors((current) => ({ ...current, [target]: next.data.error || "The install did not finish." }));
			})();
		}, INSTALL_POLL_MS);
		timers.current.set(target, timer);
	}, []);
	return { start, isRunning: (target: SystemInstallTarget) => active(jobs[target]), error: (target: SystemInstallTarget) => errors[target] };
}

function GitHubStep({ onNext }: { onNext: () => void }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const gate = useSystemRequirementsGate();
	const auth = useGitHubAuthRequirement();
	const [task1, task2] = useExampleCards();
	const install = useSystemInstall(() => {
		void queryClient.invalidateQueries({ queryKey: systemRequirementsQueryKey });
		void auth.refetch();
	});
	const gh = gate.requirements.find((requirement) => requirement.id === "gh");
	const installed = gh?.satisfied === true;
	const signedIn = auth.data?.satisfied === true;
	const checking = gate.checking || auth.isPending;
	const installing = install.isRunning("gh");
	return (
		<StepSplit
			title={t("firstRun.githubConnectTitle")}
			body={t("firstRun.githubDescription")}
			preview={
				// What gh gives a worker: a pushed branch and a pull request on its card.
				<PreviewWindow sidebar={<PreviewSidebar dimBody projects={[{ name: t("firstRun.projectPlaceholder"), active: true, sessions: [
					{ title: task1.title, status: "review_pending", minutesAgo: 3 },
					{ title: task2.title, status: "mergeable", minutesAgo: 9 },
				] }]} />}>
					<PreviewBoard
						lanes={[
							{ column: "needs_review", cards: [
								card(task1, { status: "review_pending", displayStatus: "Review pending", minutesAgo: 3, prs: [{ number: 128, state: "open" }] }),
							] },
							{ column: "ready", cards: [
								card(task2, { status: "mergeable", displayStatus: "Mergeable", minutesAgo: 9, prs: [{ number: 127, state: "open" }] }),
							] },
						]}
					/>
				</PreviewWindow>
			}
		>
			{checking ? (
				<Waiting>{t("firstRun.githubChecking")}</Waiting>
			) : (
				<>
					<ul className="overflow-hidden rounded-lg bg-card">
						<CheckRow
							label={t("firstRun.githubTitle")}
							detail={install.error("gh")}
							trailing={installed ? <RowState>{t("settings.harness.installed")}</RowState>
								: installing ? <RowSpinner />
								: (
									<Button size="sm" variant="primary" onClick={() => void install.start("gh")}>
										{t("firstRun.install")}
									</Button>
								)}
						/>
						<CheckRow
							label={t("firstRun.ghAccount")}
							detail={installed && !signedIn ? t("firstRun.ghSignInHint") : undefined}
							trailing={signedIn ? <RowState>{t("firstRun.signedIn")}</RowState> : null}
						/>
					</ul>
					{/* The existing login flow: an embedded `gh auth login` terminal. */}
					{installed && !signedIn ? <GitHubOnboardingNotice embedded /> : null}
				</>
			)}
			{/* Optional: Continue works either way, and leads once both are done. */}
			<StepAction>
				<Button variant={installed && signedIn ? "primary" : "secondary"} onClick={onNext}>{t("firstRun.continue")}</Button>
			</StepAction>
		</StepSplit>
	);
}

// ── 3. Agents ───────────────────────────────────────────────────────────────

type AgentRowState = "ready" | "signin" | "missing" | "checking";

function agentRowState(agent: AgentReadinessSnapshot): AgentRowState {
	if (agent.effectiveReadiness === "ready") return "ready";
	if (agent.installation.state === "not_installed") return "missing";
	if (agent.authentication.state === "unauthorized") return "signin";
	return "checking";
}

const AGENT_STATE_ORDER: Record<AgentRowState, number> = { ready: 0, signin: 1, checking: 2, missing: 3 };

function AgentsStep({ onNext }: { onNext: () => void }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
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
	const install = useSystemInstall(() => void refresh());

	const agents = useMemo(() => {
		const all = readiness.data?.agents ?? [];
		return all
			.filter((agent) => agent.installation.state !== "not_installed" || SUGGESTED_AGENTS.includes(agent.id))
			.map((agent) => ({ agent, state: agentRowState(agent) }))
			.sort((a, b) =>
				AGENT_STATE_ORDER[a.state] - AGENT_STATE_ORDER[b.state] ||
				(DEFAULT_AGENT_PRIORITY_RANK.get(a.agent.id) ?? 99) - (DEFAULT_AGENT_PRIORITY_RANK.get(b.agent.id) ?? 99) ||
				a.agent.label.localeCompare(b.agent.label))
			.slice(0, 6);
	}, [readiness.data]);
	const anyReady = agents.some((row) => row.state === "ready");
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
			await refresh();
		} finally {
			setChecking(false);
		}
	};

	// The New chat agent picker lists the agents that are ready; until one is,
	// it shows the candidates dimmed.
	const pickerAgents = (anyReady ? agents.filter((row) => row.state === "ready") : agents)
		.map(({ agent }) => ({ id: agent.id, label: agent.label, dimmed: !anyReady }));
	const selectedAgent = anyReady ? pickerAgents[0] : undefined;

	return (
		<StepSplit
			title={t("firstRun.agentsTitle")}
			// Ready rows speak for themselves; say more only when Continue is blocked.
			body={anyReady || readiness.isPending ? undefined : t("firstRun.agentsBodyNone")}
			preview={
				<PreviewWindow page="new" sidebar={<PreviewSidebar dimBody dimFooter projects={[{ name: t("firstRun.projectPlaceholder") }]} />}>
					<PreviewNewChat agents={pickerAgents} selectedId={selectedAgent?.id} project={t("firstRun.projectPlaceholder")} />
				</PreviewWindow>
			}
		>
			{readiness.isPending ? (
				<Waiting>{t("firstRun.agentChecking")}</Waiting>
			) : (
				<div className="flex flex-col gap-2">
					<ul className="overflow-hidden rounded-lg bg-card">
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
				{/* At least one ready agent is needed to orchestrate anything. */}
				<Button variant="primary" onClick={onNext} disabled={!anyReady}>{t("firstRun.continue")}</Button>
			</StepAction>
		</StepSplit>
	);
}

// ── 4. Tutorial: what the orchestrator is ───────────────────────────────────

function OrchestratorStep({ point, onPoint, onNext }: { point: number; onPoint: (point: number) => void; onNext: () => void }) {
	const { t } = useTranslation();
	const reduceMotion = useReducedMotion();
	const [task1, task2, task3] = useExampleCards();
	const last = point >= ORCH_POINTS.length - 1;
	const workers = (statuses: Array<Pick<PreviewSidebarSession, "status" | "working" | "minutesAgo">>): PreviewSidebarProject[] => [{
		name: t("firstRun.projectPlaceholder"),
		active: true,
		sessions: [task1, task2, task3].map((task, i) => ({ title: task.title, ...statuses[i]! })),
	}];
	const preview = [
		// 1. The user's message to the orchestrator.
		<PreviewWindow key="chat" page="session" sidebar={<PreviewSidebar projects={[{ name: t("firstRun.projectPlaceholder"), active: true, orchestratorActive: true }]} />}>
			<PreviewChat title={t("shell.orchestrator")} provider="claude-code" message={t("firstRun.exampleMessage")} reply={t("firstRun.exampleReply")} />
		</PreviewWindow>,
		// 2. Three workers appear, each on its own branch.
		<PreviewWindow
			key="workers"
			sidebar={<PreviewSidebar projects={workers([{ status: "working", working: true }, { status: "working", working: true }, { status: "working", working: true }])} />}
		>
			<PreviewBoard
				stagger
				lanes={[
					{ column: "building", cards: [
						card(task1, { status: "working", displayStatus: "Working", working: true, minutesAgo: 0 }),
						card(task2, { status: "working", displayStatus: "Working", working: true, minutesAgo: 0 }),
						card(task3, { status: "working", displayStatus: "Working", working: true, minutesAgo: 0 }),
					] },
					{ column: "needs_review", cards: [] },
				]}
			/>
		</PreviewWindow>,
		// 3. Their pull requests wait for review, then merge.
		<PreviewWindow
			key="review"
			sidebar={<PreviewSidebar projects={workers([{ status: "review_pending", minutesAgo: 6 }, { status: "mergeable", minutesAgo: 8 }, { status: "approved", minutesAgo: 11 }])} />}
		>
			<PreviewBoard
				lanes={[
					{ column: "needs_review", cards: [
						card(task1, { status: "review_pending", displayStatus: "Review pending", minutesAgo: 6, prs: [{ number: 12, state: "open" }] }),
					] },
					{ column: "ready", cards: [
						card(task2, { status: "mergeable", displayStatus: "Mergeable", minutesAgo: 8, prs: [{ number: 13, state: "open" }] }),
						card(task3, { status: "approved", displayStatus: "Approved", minutesAgo: 11, prs: [{ number: 14, state: "open" }] }),
					] },
				]}
			/>
		</PreviewWindow>,
	];
	return (
		<StepSplit
			title={t("firstRun.orchTitle")}
			preview={
				// The points cross-fade over the stage's glass plate.
				<AnimatePresence initial={false}>
					<motion.div
						key={point}
						className="absolute inset-0"
						initial={reduceMotion ? false : { opacity: 0 }}
						animate={{ opacity: 1 }}
						exit={{ opacity: 0, transition: reduceMotion ? { duration: 0 } : FADE }}
						transition={FADE}
					>
						{preview[point]}
					</motion.div>
				</AnimatePresence>
			}
		>
			<ol className="flex flex-col gap-1">
				{ORCH_POINTS.map((key, i) => (
					<li key={key}>
						<button
							type="button"
							aria-current={i === point ? "step" : undefined}
							className={cn(
								"flex w-full gap-3 rounded-md py-1.5 text-left text-sm leading-relaxed outline-none focus-visible:ring-focus focus-visible:ring-ring/60",
								i === point ? "text-foreground" : "text-muted-foreground hover:text-foreground",
							)}
							onClick={() => onPoint(i)}
						>
							<span className="w-3 shrink-0 tabular-nums">{i + 1}</span>
							<span className="text-pretty">{t(key)}</span>
						</button>
					</li>
				))}
			</ol>
			<StepAction>
				<Button variant="primary" onClick={last ? onNext : () => onPoint(point + 1)}>
					{t(last ? "firstRun.continue" : "firstRun.next")}
				</Button>
			</StepAction>
		</StepSplit>
	);
}

// ── 5. Tutorial: set up a project ───────────────────────────────────────────

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

function ProjectStep({ onDone }: { onDone: () => void }) {
	const { t } = useTranslation();
	const { cloneProject, createProject, initializeProjectRepository } = useShell();
	const projects = useWorkspaceQuery().data ?? [];
	const localProjects = useLocalProjects();
	const [sourceSignal, setSourceSignal] = useState<{ source: ProjectSource; nonce: number } | null>(null);
	const request = (source: ProjectSource) => setSourceSignal({ source, nonce: Date.now() });

	return (
		<StepSplit
			title={t("firstRun.projectTitle")}
			body={t("firstRun.projectBody")}
			preview={
				// The sidebar's Projects list, where the new project lands.
				<PreviewWindow
					sidebar={
						<PreviewSidebar
							projects={[
								...localProjects.map((project) => ({ name: project.name })),
								{ name: t("firstRun.projectPlaceholder"), active: true },
							]}
						/>
					}
				>
					<PreviewBoard lanes={[]} />
				</PreviewWindow>
			}
		>
			{/* Adding a project moves on by itself; Continue holds the strip steady. */}
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
				onCreateProject={async (input) => {
					await createProject(input);
					onDone();
				}}
				onInitializeProject={async (path) => {
					await initializeProjectRepository(path);
					onDone();
				}}
				sourceSignal={sourceSignal}
			/>
		</StepSplit>
	);
}

// ── 6. Tutorial: start orchestrating ────────────────────────────────────────

function StartStep({ onFinish }: { onFinish: () => void }) {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const workspaces = useWorkspaceQuery().data ?? [];
	const project = useLocalProjects()[0];
	const orchestrator = project ? findProjectOrchestrator(workspaces, project.id) : undefined;
	const provisioning = useUiStore((state) => (project ? state.provisioningProjectIds.has(project.id) : false));
	const completeTask = useOnboardingStep("first_task", true);
	const [prompt, setPrompt] = useState(() => t(STARTER_PROMPTS[0].prompt));

	const send = () => {
		if (!project) return;
		completeTask();
		onFinish();
		if (!orchestrator) {
			// Setup failed or is still running: the board shows its progress.
			void navigate({ to: "/projects/$projectId", params: { projectId: project.id } });
			return;
		}
		// The live chat scopes its draft by session id + createdAt (ChatWorkspace).
		writeChatComposerText({ sessionId: orchestrator.id, incarnation: orchestrator.createdAt ?? orchestrator.id }, prompt.trim());
		void navigate(sessionNavigateTarget(project.id, orchestrator.id));
	};

	return (
		<StepSplit
			title={t("firstRun.startTitle")}
			body={project ? t("firstRun.startBody", { project: project.name }) : undefined}
			preview={
				// The orchestrator's chat, with the first message as it will be sent.
				<PreviewWindow
					page="session"
					sidebar={
						<PreviewSidebar
							projects={[{
								name: project?.name ?? t("firstRun.projectPlaceholder"),
								active: true,
								orchestratorActive: true,
							}]}
						/>
					}
				>
					{/* Until the orchestrator exists, show it with the default agent. */}
					<PreviewChat title={t("shell.orchestrator")} provider={orchestrator?.provider ?? "claude-code"} message={prompt.trim() || undefined} />
				</PreviewWindow>
			}
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
			{!project || provisioning || !orchestrator ? <Waiting>{t("firstRun.settingUp")}</Waiting> : null}
			<StepAction>
				<Button variant="primary" onClick={send} disabled={!project || !prompt.trim() || (provisioning && !orchestrator)}>
					{t("firstRun.openOrchestrator")}
				</Button>
			</StepAction>
		</StepSplit>
	);
}
