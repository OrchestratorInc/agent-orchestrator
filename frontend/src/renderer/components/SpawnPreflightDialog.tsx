import { useState } from "react";
import { ExternalLink } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useAgentReadinessQuery, useEnsureAgentReadiness } from "../hooks/useAgentReadinessQuery";
import { aoBridge } from "../lib/bridge";
import {
	checkSpawnPreflight,
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
						<CheckSection key={check.code} check={check} agentLabel={harnessLabel} onCopy={(copied) => setMethod(copied)} />
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
	onCopy,
}: {
	check: SpawnPreflightCheck;
	agentLabel: string;
	onCopy: (method: SpawnPreflightMethod) => void;
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
			{check.command || check.documentationUrl ? (
				<div className="mt-1 flex flex-col gap-1.5">
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
