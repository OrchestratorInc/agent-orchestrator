import { useEffect, useRef, useState, type ReactNode } from "react";
import { Download, ExternalLink, LogIn } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useAgentReadinessQuery, useEnsureAgentReadiness } from "../hooks/useAgentReadinessQuery";
import { useStartAgentAuth } from "../hooks/useAgentAuth";
import type { AgentId } from "../lib/agent-options";
import { aoBridge } from "../lib/bridge";
import {
	checkSpawnPreflight,
	runPreflightInstall,
	setProjectDefaultBranch,
	setProjectOrchestratorAgent,
	useSpawnPreflightStore,
	type PendingSpawnPreflight,
	type SpawnPreflightCheck,
	type SpawnPreflightInput,
	type SpawnPreflightMethod,
} from "../lib/spawn-preflight";
import { cn } from "../lib/utils";
import { IssueSection, ManualCommand } from "./InstallDependencyDialog";
import { closeAuthTerminal, HarnessAuthTerminalPanel, type AuthTerminalWorkflow } from "./settings/harness/HarnessAuthTerminalPanel";
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
import { Input } from "./ui/input";
import { Label } from "./ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select";

const AGENT_CHECKS = new Set<SpawnPreflightCheck["code"]>(["agent_not_installed", "agent_unauthorized", "agent_required"]);

/** The single, store-driven fix dialog for a blocked spawn pre-flight. */
export function SpawnPreflightDialog() {
	const pending = useSpawnPreflightStore((state) => state.pending);
	if (!pending) return null;
	return <SpawnPreflightBody pending={pending} />;
}

function SpawnPreflightBody({ pending }: { pending: PendingSpawnPreflight }) {
	const { t } = useTranslation();
	const { input, report } = pending;
	const readiness = useAgentReadinessQuery();
	useEnsureAgentReadiness();
	// The most recent fix the user reached for; the next re-check reports it.
	const [method, setMethod] = useState<SpawnPreflightMethod>("recheck");
	const [branch, setBranch] = useState("");
	const [agent, setAgent] = useState("");
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string | null>(null);

	const startAuth = useStartAgentAuth();
	const [installing, setInstalling] = useState<SpawnPreflightCheck["code"] | null>(null);
	const [authWorkflow, setAuthWorkflow] = useState<AuthTerminalWorkflow | null>(null);
	const authWorkflowRef = useRef<AuthTerminalWorkflow | null>(null);
	authWorkflowRef.current = authWorkflow;
	// A sign-in terminal must not outlive the dialog that opened it.
	useEffect(() => () => {
		const workflow = authWorkflowRef.current;
		if (workflow) void closeAuthTerminal(workflow.terminal.handleId).catch(() => undefined);
	}, []);

	const agents = readiness.data?.agents ?? [];
	const labelFor = (id?: string) => agents.find((candidate) => candidate.id === id)?.label ?? id ?? "";
	const harnessLabel = labelFor(report.harness);
	const alternatives = agents.filter((candidate) => candidate.effectiveReadiness === "ready" && candidate.id !== report.harness);
	const needsAgent = report.checks.some((check) => AGENT_CHECKS.has(check.code));

	const recheck = async (nextMethod: SpawnPreflightMethod, nextInput: SpawnPreflightInput = input) => {
		setBusy(true);
		setError(null);
		try {
			const next = await checkSpawnPreflight(nextInput, { blockers: report.checks.map((check) => check.code), method: nextMethod });
			const store = useSpawnPreflightStore.getState();
			if (next.ready) store.settle({ harness: nextInput.harness ?? next.harness });
			else {
				store.update(next, nextInput);
				setMethod("recheck");
			}
		} catch {
			setError(t("spawnPreflight.checkFailed"));
		} finally {
			setBusy(false);
		}
	};

	const run = async (action: () => Promise<void>, nextMethod: SpawnPreflightMethod, nextInput?: SpawnPreflightInput) => {
		setBusy(true);
		setError(null);
		try {
			await action();
		} catch (cause) {
			setError(cause instanceof Error ? cause.message : t("spawnPreflight.checkFailed"));
			setBusy(false);
			return;
		}
		await recheck(nextMethod, nextInput);
	};

	// AO runs the install itself, then re-checks as soon as the job ends.
	const install = async (check: SpawnPreflightCheck) => {
		setBusy(true);
		setInstalling(check.code);
		setError(null);
		try {
			const job = await runPreflightInstall(check, report.harness);
			if (job.status !== "succeeded") {
				setError(job.error || t("spawnPreflight.installFailed"));
				setBusy(false);
				return;
			}
		} catch (cause) {
			setError(cause instanceof Error ? cause.message : t("spawnPreflight.installFailed"));
			setBusy(false);
			return;
		} finally {
			setInstalling(null);
		}
		await recheck("install");
	};

	// The harness's own reviewed login runs in an embedded terminal; when it
	// exits, the terminal closes and the pre-flight re-checks on its own.
	const signIn = async () => {
		if (!report.harness) return;
		setBusy(true);
		setError(null);
		try {
			const result = await startAuth.mutateAsync(report.harness);
			setAuthWorkflow({
				agentId: report.harness as AgentId,
				action: result.action,
				terminal: result.terminal,
				guidance: result.guidance ?? "",
				terminalInput: result.terminalInput,
				phase: "running",
				startedAt: Date.now(),
			});
		} catch (cause) {
			setError(cause instanceof Error ? cause.message : t("spawnPreflight.signInFailed"));
		} finally {
			setBusy(false);
		}
	};

	const closeSignIn = async (workflow: AuthTerminalWorkflow) => {
		setAuthWorkflow(null);
		await closeAuthTerminal(workflow.terminal.handleId).catch(() => undefined);
	};

	const finishSignIn = async (workflow: AuthTerminalWorkflow) => {
		setAuthWorkflow((current) => current?.terminal.handleId === workflow.terminal.handleId ? { ...current, phase: "verifying" } : current);
		await closeAuthTerminal(workflow.terminal.handleId).catch(() => undefined);
		setAuthWorkflow(null);
		await recheck("login");
	};

	const useAgent = () => {
		if (!agent) return;
		// An orchestrator's agent lives in project config so every later start
		// uses it; a worker spawn names its harness directly.
		const nextInput = { ...input, harness: input.kind === "worker" || input.harness ? agent : undefined };
		void run(
			input.kind === "orchestrator" ? () => setProjectOrchestratorAgent(input.projectId, agent) : async () => undefined,
			"switch_agent",
			nextInput,
		);
	};

	const saveBranch = () => {
		const value = branch.trim();
		if (value) void run(() => setProjectDefaultBranch(input.projectId, value), "set_default_branch");
	};

	return (
		<Dialog open onOpenChange={(open) => !open && useSpawnPreflightStore.getState().settle(null)}>
			<DialogContent className={settingsDialogContentClass}>
				<div className={settingsDialogHeaderClass}>
					<DialogTitle className="settings-dialog-title">
						{input.kind === "orchestrator" ? t("spawnPreflight.titleOrchestrator") : t("spawnPreflight.titleWorker")}
					</DialogTitle>
					<DialogDescription className="sr-only">{t("spawnPreflight.description")}</DialogDescription>
				</div>
				<div className={cn(settingsDialogBodyClass, "gap-5")}>
					{report.checks.map((check) => (
						<CheckSection
							key={check.code}
							check={check}
							agentLabel={harnessLabel}
							busy={busy || authWorkflow !== null}
							installing={installing === check.code}
							onCopy={(copied) => setMethod(copied)}
							onInstall={() => void install(check)}
							onSignIn={() => void signIn()}
						>
							{check.code === "agent_unauthorized" && authWorkflow ? (
								<HarnessAuthTerminalPanel
									workflow={authWorkflow}
									onClose={() => void closeSignIn(authWorkflow)}
									onRetry={() => void closeSignIn(authWorkflow).then(signIn)}
									onTerminalState={(state) => {
										if (state === "exited" && authWorkflowRef.current?.phase === "running") void finishSignIn(authWorkflowRef.current);
									}}
								/>
							) : null}
						</CheckSection>
					))}
					{report.checks.some((check) => check.code === "default_branch_unresolved") ? (
						<div className="flex flex-col gap-1.5">
							<Label htmlFor="spawn-preflight-branch">{t("spawnPreflight.branchLabel")}</Label>
							<div className="flex items-center gap-2">
								<Input
									id="spawn-preflight-branch"
									value={branch}
									placeholder="main"
									autoComplete="off"
									spellCheck={false}
									className="font-mono"
									onChange={(event) => setBranch(event.target.value)}
									onKeyDown={(event) => event.key === "Enter" && saveBranch()}
								/>
								<button type="button" className="settings-footer-button shrink-0" disabled={busy || !branch.trim()} onClick={saveBranch}>
									{t("spawnPreflight.saveBranch")}
								</button>
							</div>
						</div>
					) : null}
					{needsAgent ? (
						<div className="flex flex-col gap-1.5">
							<Label htmlFor="spawn-preflight-agent">{t("spawnPreflight.useAnotherAgent")}</Label>
							{alternatives.length > 0 ? (
								<div className="flex items-center gap-2">
									<Select value={agent} onValueChange={setAgent}>
										<SelectTrigger id="spawn-preflight-agent" className="min-w-0 flex-1">
											<SelectValue placeholder={t("spawnPreflight.chooseAgent")} />
										</SelectTrigger>
										<SelectContent>
											{alternatives.map((candidate) => (
												<SelectItem key={candidate.id} value={candidate.id}>{candidate.label}</SelectItem>
											))}
										</SelectContent>
									</Select>
									<button type="button" className="settings-footer-button shrink-0" disabled={busy || !agent} onClick={useAgent}>
										{t("spawnPreflight.useAgent")}
									</button>
								</div>
							) : (
								<p id="spawn-preflight-agent" className="text-caption text-settings-muted">{t("spawnPreflight.noOtherAgents")}</p>
							)}
						</div>
					) : null}
				</div>
				<div className={settingsDialogFooterClass}>
					{error ? <p role="alert" className="basis-full text-caption leading-4 text-error">{error}</p> : null}
					{report.canProceed ? (
						<button
							type="button"
							className="settings-footer-button"
							disabled={busy}
							onClick={() => useSpawnPreflightStore.getState().settle({ harness: input.harness ?? report.harness })}
						>
							{t("spawnPreflight.startAnyway")}
						</button>
					) : null}
					<button
						type="button"
						className="settings-footer-button settings-footer-button-primary"
						disabled={busy}
						onClick={() => void recheck(method)}
					>
						{busy ? t("startup.checkingAgain") : t("startup.checkAgain")}
					</button>
				</div>
			</DialogContent>
		</Dialog>
	);
}

function CheckSection({
	check,
	agentLabel,
	busy,
	installing,
	onCopy,
	onInstall,
	onSignIn,
	children,
}: {
	check: SpawnPreflightCheck;
	agentLabel: string;
	busy: boolean;
	installing: boolean;
	onCopy: (method: SpawnPreflightMethod) => void;
	onInstall: () => void;
	onSignIn: () => void;
	children?: ReactNode;
}) {
	const { t } = useTranslation();
	const copy = {
		agent_not_installed: {
			label: t("spawnPreflight.agentNotInstalled", { agent: agentLabel }),
			detail: t("spawnPreflight.agentNotInstalledDetail"),
			method: "install" as const,
		},
		agent_unauthorized: {
			label: t("spawnPreflight.agentUnauthorized", { agent: agentLabel }),
			detail: check.blocking ? t("spawnPreflight.agentUnauthorizedDetail") : t("spawnPreflight.agentUnauthorizedSoftDetail"),
			method: "login" as const,
		},
		runtime_prerequisite_missing: {
			label: t("spawnPreflight.runtimeMissing"),
			detail: t("spawnPreflight.runtimeMissingDetail"),
			method: "install" as const,
		},
		default_branch_unresolved: {
			label: t("spawnPreflight.branchUnresolved"),
			detail: t("spawnPreflight.branchUnresolvedDetail"),
			method: "set_default_branch" as const,
		},
		agent_required: {
			label: t("spawnPreflight.agentRequired"),
			detail: t("spawnPreflight.agentRequiredDetail"),
			method: "switch_agent" as const,
		},
	}[check.code];
	return (
		<IssueSection label={copy.label} detail={copy.detail}>
			{check.command || check.documentationUrl || check.canInstall || check.canStartLogin || children ? (
				<div className="mt-1 flex flex-col gap-1.5">
					{check.canInstall ? (
						<button type="button" className="settings-footer-button self-start" disabled={busy} onClick={onInstall}>
							<Download className="size-icon-sm" aria-hidden="true" />
							{installing ? t("spawnPreflight.installing") : t("spawnPreflight.install")}
						</button>
					) : null}
					{check.canStartLogin && !children ? (
						<button type="button" className="settings-footer-button self-start" disabled={busy} onClick={onSignIn}>
							<LogIn className="size-icon-sm" aria-hidden="true" />
							{t("spawnPreflight.signIn")}
						</button>
					) : null}
					{children}
					{check.command ? <ManualCommand command={check.command} t={t} onCopy={() => onCopy(copy.method)} /> : null}
					{check.documentationUrl ? (
						<button
							type="button"
							className="inline-flex items-center gap-1 self-start text-caption text-settings-muted underline-offset-2 hover:underline"
							onClick={() => void aoBridge.app.openExternal(check.documentationUrl!)}
						>
							{t("spawnPreflight.setupGuide")}
							<ExternalLink className="size-icon-xs" aria-hidden="true" />
						</button>
					) : null}
				</div>
			) : null}
		</IssueSection>
	);
}
