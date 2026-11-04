import { ArrowUpRight, Check, Copy, LoaderCircle } from "lucide-react";
import type { TFunction } from "i18next";
import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../api/schema";
import type { MessageKey } from "../i18n";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { aoBridge } from "../lib/bridge";
import { cn } from "../lib/utils";
import type { SystemRequirement } from "./SystemRequirementsChecklist";
import { Button } from "./ui/button";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogTitle,
	settingsDialogBodyClass,
	settingsDialogContentClass,
	settingsDialogFooterClass,
	settingsDialogHeaderClass,
} from "./ui/dialog";

type InstallJob = components["schemas"]["InstallJob"];
// `notice` is emitted by the daemon's OpenCode 2 install plan. Keep this
// narrow compatibility extension while older generated clients are in use;
// it becomes part of InstallJob when the accompanying API schema regenerates.
type InstallJobWithNotice = InstallJob & { notice?: string };
type InstallTarget = "tmux" | "gh" | "claude" | "codex" | "opencode" | "opencode-v2" | "copilot";
type AgentInstallTarget = Exclude<InstallTarget, "tmux" | "gh">;

// Labels are the CLIs' own product names — not translated, same treatment as
// "Agent Orchestrator" itself. Descriptions are ordinary UI copy and go
// through t() at render time (see AGENT_INSTALL_DESCRIPTION_KEYS below).
const AGENT_INSTALL_OPTIONS: Array<{ target: AgentInstallTarget; label: string }> = [
	{ target: "claude", label: "Claude Code" },
	{ target: "codex", label: "Codex" },
	{ target: "opencode", label: "opencode" },
	{ target: "opencode-v2", label: "OpenCode 2" },
	{ target: "copilot", label: "Copilot CLI" },
];

const AGENT_INSTALL_DESCRIPTION_KEYS: Record<AgentInstallTarget, MessageKey> = {
	claude: "startup.agentDescClaude",
	codex: "startup.agentDescCodex",
	opencode: "startup.agentDescOpencode",
	"opencode-v2": "startup.agentDescOpencode",
	copilot: "startup.agentDescCopilot",
};

const POLL_INTERVAL_MS = 1_000;
const GIT_DOWNLOAD_URL = "https://git-scm.com/downloads";

export function isActiveInstallJob(job: InstallJob | undefined): boolean {
	return job?.status === "running" || job?.status === "installing" || job?.status === "verifying";
}

// Startup requirements are read through a process-free GET. An explicit user
// request to check again first forces the daemon's normal agent refresh so
// identity-sensitive adapters can perform their bounded validation probe.
export async function checkRequirementsAgain(onRefetchRequirements: () => Promise<unknown> | void): Promise<void> {
	const { error } = await apiClient.POST("/api/v1/agents/refresh");
	if (error) throw new Error(apiErrorMessage(error, "Could not refresh agent inventory."));
	await onRefetchRequirements();
}

/** Sequential single-target install job runner: POST to start, GET on an
 *  interval while running. One target is ever in flight at a time — this
 *  gate only ever needs one, and serializing keeps the UI unambiguous about
 *  which command is running. */
function useInstallRunner(onSucceeded: () => void) {
	const [target, setTarget] = useState<InstallTarget | null>(null);
	const [job, setJob] = useState<InstallJobWithNotice | undefined>(undefined);
	const [previews, setPreviews] = useState<Partial<Record<InstallTarget, InstallJobWithNotice>>>({});
	const [inspectedTargets, setInspectedTargets] = useState<Partial<Record<InstallTarget, boolean>>>({});
	const [isStarting, setIsStarting] = useState(false);
	const [startError, setStartError] = useState<string | undefined>(undefined);
	const [succeededTargets, setSucceededTargets] = useState<ReadonlySet<InstallTarget>>(new Set());
	const pollRef = useRef<number | null>(null);
	const inspectedRef = useRef(new Set<InstallTarget>());
	const onSucceededRef = useRef(onSucceeded);
	onSucceededRef.current = onSucceeded;
	const markSucceeded = (doneTarget: InstallTarget) => {
		setSucceededTargets((current) => new Set(current).add(doneTarget));
		onSucceededRef.current();
	};

	const stopPolling = () => {
		if (pollRef.current !== null) {
			window.clearInterval(pollRef.current);
			pollRef.current = null;
		}
	};
	useEffect(() => stopPolling, []);

	const poll = (polledTarget: InstallTarget) => {
		stopPolling();
		pollRef.current = window.setInterval(() => {
			void (async () => {
				const { data, error } = await apiClient.GET("/api/v1/system/install/{target}", {
					params: { path: { target: polledTarget } },
				});
				if (error || !data) return; // transient — try again next tick
				setJob(data as InstallJobWithNotice);
				if (isActiveInstallJob(data)) return;
				stopPolling();
				if (data.status === "succeeded") markSucceeded(polledTarget);
			})();
		}, POLL_INTERVAL_MS);
	};

	// GET doubles as a safe install-plan preview. In particular, Linux returns
	// Unsupported plus the exact sudo command, so the renderer can show manual
	// instructions without first POSTing a job that is guaranteed to fail.
	const inspect = useCallback(async (nextTarget: InstallTarget) => {
		if (inspectedRef.current.has(nextTarget)) return;
		inspectedRef.current.add(nextTarget);
		try {
			const { data, error } = await apiClient.GET("/api/v1/system/install/{target}", {
				params: { path: { target: nextTarget } },
			});
			if (error || !data) return;
			setPreviews((current) => ({ ...current, [nextTarget]: data as InstallJobWithNotice }));
		} catch {
			// The requirements gate remains usable if plan inspection fails. The
			// POST action will still surface its own concrete error when selected.
		} finally {
			setInspectedTargets((current) => ({ ...current, [nextTarget]: true }));
		}
	}, []);

	const start = async (nextTarget: InstallTarget) => {
		stopPolling();
		setTarget(nextTarget);
		setJob(undefined);
		setStartError(undefined);
		setIsStarting(true);
		try {
			const { data, error } = await apiClient.POST("/api/v1/system/install/{target}", {
				params: { path: { target: nextTarget } },
			});
			if (error || !data) throw new Error(apiErrorMessage(error, "Could not start the install."));
			setJob(data as InstallJobWithNotice);
			if (isActiveInstallJob(data)) poll(nextTarget);
			else if (data.status === "succeeded") markSucceeded(nextTarget);
		} catch (err) {
			setStartError(err instanceof Error ? err.message : "Could not start the install.");
		} finally {
			setIsStarting(false);
		}
	};

	const running = isStarting || isActiveInstallJob(job);
	const jobFor = (nextTarget: InstallTarget) => (target === nextTarget ? job : previews[nextTarget]);
	const inspectionFinished = (nextTarget: InstallTarget) => inspectedTargets[nextTarget] === true;
	const succeeded = (nextTarget: InstallTarget) => succeededTargets.has(nextTarget);
	return { target, startError, running, start, inspect, jobFor, inspectionFinished, succeeded };
}

export function InstallDependencyDialog({
	requirements,
	onRefetchRequirements,
}: {
	requirements: SystemRequirement[];
	onRefetchRequirements: () => Promise<unknown> | void;
}) {
	const { t } = useTranslation();
	const [isCheckingAgain, setIsCheckingAgain] = useState(false);
	const [checkAgainError, setCheckAgainError] = useState<string | undefined>();
	const install = useInstallRunner(() => void onRefetchRequirements());

	const byId = new Map(requirements.map((requirement) => [requirement.id, requirement]));
	const git = byId.get("git");
	const tmux = byId.get("tmux");
	const harness = byId.get("harness");
	const gh = byId.get("gh");
	const gitBlocking = Boolean(git && git.required && !git.satisfied);
	const tmuxBlocking = Boolean(tmux && tmux.required && !tmux.satisfied);
	const harnessBlocking = Boolean(harness && harness.required && !harness.satisfied);
	// gh stays listed after its install so the row settles on Installed
	// instead of vanishing while the rest of the gate is still open.
	const showGh = Boolean(gh && (!gh.satisfied || install.succeeded("gh")));
	const { inspect } = install;

	useEffect(() => {
		if (tmuxBlocking) void inspect("tmux");
	}, [inspect, tmuxBlocking]);

	useEffect(() => {
		if (showGh) void inspect("gh");
	}, [inspect, showGh]);

	useEffect(() => {
		if (!harnessBlocking) return;
		for (const option of AGENT_INSTALL_OPTIONS) void inspect(option.target);
	}, [inspect, harnessBlocking]);

	const checkAgain = async () => {
		setIsCheckingAgain(true);
		setCheckAgainError(undefined);
		try {
			await checkRequirementsAgain(onRefetchRequirements);
		} catch (error) {
			setCheckAgainError(
				error instanceof Error && error.message ? error.message : "Could not refresh agent inventory.",
			);
		} finally {
			setIsCheckingAgain(false);
		}
	};

	const title = harnessBlocking && !gitBlocking && !tmuxBlocking ? t("startup.blockedTitleAgent") : t("startup.blockedTitleDependency");
	const installRow = (target: InstallTarget, label: string, purpose: string, notice?: string) => (
		<InstallRow
			key={target}
			label={label}
			purpose={purpose}
			notice={notice}
			job={install.jobFor(target)}
			planChecked={install.inspectionFinished(target)}
			succeeded={install.succeeded(target)}
			disabled={install.running && install.target !== target}
			error={install.target === target ? install.startError : undefined}
			onInstall={() => void install.start(target)}
			t={t}
		/>
	);

	return (
		<Dialog open onOpenChange={() => {}}>
			<DialogContent
				showCloseButton={false}
				className={settingsDialogContentClass}
				onEscapeKeyDown={(event) => event.preventDefault()}
				onPointerDownOutside={(event) => event.preventDefault()}
			>
				<div className={settingsDialogHeaderClass}>
					<DialogTitle className="settings-dialog-title">{title}</DialogTitle>
					<DialogDescription className="text-control leading-4 text-settings-muted">
						{t("startup.blockedBodyShort")}
					</DialogDescription>
				</div>

				<div className={cn(settingsDialogBodyClass, "gap-4")}>
					{gitBlocking || tmuxBlocking || showGh ? (
						<div className="settings-grouped-rows">
							{gitBlocking ? (
								<RequirementRow label="git" purpose={t("startup.gitPurpose")}>
									<Button
										type="button"
										size="sm"
										variant="ghost"
										onClick={() => void aoBridge.app.openExternal(GIT_DOWNLOAD_URL)}
									>
										{t("startup.getGit")}
										<ArrowUpRight className="size-icon-sm text-settings-muted" aria-hidden="true" />
									</Button>
								</RequirementRow>
							) : null}
							{tmuxBlocking ? installRow("tmux", "tmux", t("startup.tmuxPurpose")) : null}
							{showGh ? installRow("gh", "GitHub CLI", t("startup.ghPurpose")) : null}
						</div>
					) : null}

					{harnessBlocking ? (
						<section className="flex flex-col gap-1.5" aria-label={t("startup.chooseAgentAriaLabel")}>
							<div className="px-(--settings-row-inset)">
								<p className="text-control font-medium text-settings-title">{t("startup.codingAgentLabel")}</p>
								<p className="text-caption text-settings-muted">{t("startup.agentPurpose")}</p>
							</div>
							<div className="settings-grouped-rows">
								{AGENT_INSTALL_OPTIONS.map((option) =>
									installRow(
										option.target,
										option.label,
										t(AGENT_INSTALL_DESCRIPTION_KEYS[option.target]),
										option.target === "opencode-v2" ? install.jobFor("opencode-v2")?.notice : undefined,
									),
								)}
							</div>
						</section>
					) : null}
				</div>

				<div className={settingsDialogFooterClass}>
					{checkAgainError ? (
						<p role="alert" className="basis-full text-caption leading-4 text-error">
							{checkAgainError}
						</p>
					) : null}
					<button
						type="button"
						className="settings-footer-button"
						onClick={() => void window.ao?.menu?.action("app.quit")}
					>
						{t("startup.quit")}
					</button>
					<button
						type="button"
						className="settings-footer-button settings-footer-button-primary"
						disabled={isCheckingAgain || install.running}
						onClick={() => void checkAgain()}
					>
						{isCheckingAgain ? t("startup.checkingAgain") : t("startup.checkAgain")}
					</button>
				</div>
			</DialogContent>
		</Dialog>
	);
}

/** One item in the shared list: name and why it is needed on the left, a
 * single compact action on the right, and any progress or failure below. */
function RequirementRow({
	label,
	purpose,
	children,
	detail,
}: {
	label: string;
	purpose: string;
	children?: React.ReactNode;
	detail?: React.ReactNode;
}) {
	return (
		<div>
			<div className="settings-row-bar">
				<div className="min-w-0">
					<p className="text-control font-medium text-settings-title">{label}</p>
					<p className="text-caption text-settings-muted">{purpose}</p>
				</div>
				<div className="flex shrink-0 items-center">{children}</div>
			</div>
			{detail ? <div className="settings-content-row flex flex-col gap-1.5 pt-0">{detail}</div> : null}
		</div>
	);
}

function InstallRow({
	label,
	purpose,
	notice,
	job,
	planChecked,
	succeeded,
	disabled,
	error,
	onInstall,
	t,
}: {
	label: string;
	purpose: string;
	notice?: string;
	job: InstallJobWithNotice | undefined;
	planChecked: boolean;
	succeeded: boolean;
	disabled: boolean;
	error: string | undefined;
	onInstall: () => void;
	t: TFunction;
}) {
	const running = isActiveInstallJob(job);
	const failed = job?.status === "failed";
	const unsupported = job?.status === "unsupported";

	let action: React.ReactNode;
	if (succeeded) action = <span className="text-caption text-settings-muted">{t("settings.harness.installed")}</span>;
	else if (running || !planChecked) {
		action = (
			<span className="flex items-center gap-1.5 text-caption text-settings-muted">
				<LoaderCircle className="size-icon-sm animate-spin" aria-hidden="true" />
				{running ? t("startup.installingEllipsis") : null}
			</span>
		);
	} else if (!unsupported) {
		action = (
			<Button type="button" size="sm" variant="ghost" disabled={disabled} onClick={onInstall}>
				{failed ? t("startup.githubLoginTryAgain") : t("firstRun.install")}
			</Button>
		);
	}

	const detail = succeeded ? null : (
		<>
			{notice && !running ? <p className="text-caption text-warning" role="status">{notice}</p> : null}
			{running ? (
				<div className="ao-install-progress" aria-hidden="true">
					<div className="ao-install-progress__bar" />
				</div>
			) : null}
			{unsupported ? (
				<>
					{job.error ? <p className="text-caption text-settings-muted">{job.error}</p> : null}
					{job.command ? <ManualCommand command={job.command} t={t} /> : null}
				</>
			) : null}
			{error ? <p className="text-caption text-error">{error}</p> : null}
			{failed && job?.error ? <p className="text-caption text-error">{job.error}</p> : null}
			{failed && job?.output ? (
				<pre className="max-h-daemon-failure-details-max overflow-auto rounded-md bg-[var(--color-bg-settings-input)] px-2 py-1.5 font-mono text-caption leading-relaxed text-settings-muted">
					{job.output}
				</pre>
			) : null}
		</>
	);
	const hasDetail = !succeeded && Boolean((notice && !running) || running || unsupported || error || failed);

	return (
		<RequirementRow label={label} purpose={purpose} detail={hasDetail ? detail : undefined}>
			{action}
		</RequirementRow>
	);
}

function ManualCommand({ command, t }: { command: string; t: TFunction }) {
	const [copied, setCopied] = useState(false);
	const resetTimer = useRef<ReturnType<typeof setTimeout>>(undefined);

	useEffect(() => () => clearTimeout(resetTimer.current), []);

	const copy = () => {
		void aoBridge.clipboard.writeText(command).then(
			() => {
				setCopied(true);
				clearTimeout(resetTimer.current);
				resetTimer.current = setTimeout(() => setCopied(false), 1_400);
			},
			() => undefined,
		);
	};

	return (
		<div className="flex items-center gap-2 rounded-md border border-(--color-border-settings-input) bg-[var(--color-bg-settings-input)] px-2 py-1.5">
			<code className="min-w-0 flex-1 overflow-x-auto whitespace-nowrap font-mono text-caption text-settings-title">
				{command}
			</code>
			<button
				type="button"
				className="settings-footer-button shrink-0"
				aria-label={copied ? t("startup.commandCopied") : t("startup.copyCommand")}
				onClick={copy}
			>
				{copied ? <Check className="size-icon-sm" aria-hidden="true" /> : <Copy className="size-icon-sm" aria-hidden="true" />}
				{copied ? t("startup.commandCopied") : t("startup.copyCommand")}
			</button>
		</div>
	);
}
