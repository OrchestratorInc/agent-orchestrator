// Pieces both cloud creation cards share (CreateProjectFlow's CloudProjectCard
// and CloudWorkspaceCard): the GitHub App connection, the Coder template
// fields, and the agent step that creates the project.

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useCloudCp } from "../hooks/useCloudCp";
import { useCloudGitHubAppRepositories } from "../hooks/useCloudGitHubAppRepositories";
import { useCloudOrg } from "../hooks/useCloudOrg";
import { useCoderTemplates } from "../hooks/useCoderTemplates";
import { useOrgCoderConfig } from "../hooks/useOrgCoderConfig";
import { useProviderConnections } from "../hooks/useProviderConnections";
import { cloudProjectsQueryKey } from "../hooks/useWorkspaceQuery";
import { aoBridge } from "../lib/bridge";
import { cloudAgentInfos } from "../lib/cloud-agents";
import { CloudCpError, type CloudCpProject } from "../lib/cloud-cp";
import { FREESTYLE_PROVIDER, projectSandboxProvider, type ResolvedCloudOffering } from "../lib/cloud-offering";
import { coderWorkspaceNamePrefixFor, isValidCoderWorkspaceNamePrefix } from "../lib/coder-workspace-name";
import { onboardingAlertErrorClass, onboardingFieldErrorClass, onboardingFieldHintClass, onboardingFooterActionsClass } from "../lib/onboarding-ui";
import { buildCoderRequestOptions, useCoderSessionOptionsStore } from "../stores/coder-session-options-store";
import { useSidebarOrderStore } from "../stores/sidebar-order-store";
import { useUiStore } from "../stores/ui-store";
import { CoderTemplateOptions } from "./CoderTemplateOptions";
import { CoderTemplatePicker } from "./CoderTemplatePicker";
import { RequiredAgentField } from "./CreateProjectAgentSheet";
import { GitHubIcon } from "./GitHubIcon";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { Label } from "./ui/label";
import { Pressable } from "./ui/pressable";
import { Tabs, TabsList, TabsTrigger } from "./ui/tabs";

// sleepWithAbort resolves after `ms`, or rejects with an AbortError the moment
// the signal aborts, so a polling loop can stop immediately on unmount/cancel.
export function sleepWithAbort(ms: number, signal: AbortSignal): Promise<void> {
	return new Promise<void>((resolve, reject) => {
		if (signal.aborted) {
			reject(new DOMException("Aborted", "AbortError"));
			return;
		}
		const id = setTimeout(() => {
			signal.removeEventListener("abort", onAbort);
			resolve();
		}, ms);
		const onAbort = () => {
			clearTimeout(id);
			reject(new DOMException("Aborted", "AbortError"));
		};
		signal.addEventListener("abort", onAbort, { once: true });
	});
}

// Cloud project creation goes straight to the control plane
// (client.createGitHubProject) instead of the daemon POST the local flow uses;
// the repository is cloned in a cloud sandbox, so no folder picker.

/** Second half of cloud project creation: which agent runs the worker and
 * which plans as orchestrator. Reuses the exact field local's own agent
 * sheet uses (RequiredAgentField) against cloud provider-connection state
 * instead of daemon agent readiness — same component, different source of
 * truth, per the onboarding design. */
export function CloudAgentSetupStep({
	onBack,
	onCreate,
	isCreating,
	createError,
	templateMissing,
	createBlocked = false,
}: {
	onBack: () => void;
	onCreate: (selection: { workerAgent: string; orchestratorAgent: string }) => void;
	isCreating: boolean;
	createError: string | null;
	templateMissing: boolean;
	/** Another field (e.g. an invalid Coder workspace prefix) blocks create; it shows its own error. */
	createBlocked?: boolean;
}) {
	const { t } = useTranslation();
	const connections = useProviderConnections();
	const openGlobalSettings = useUiStore((state) => state.openGlobalSettings);
	const cloudAgents = useMemo(() => cloudAgentInfos(connections.data), [connections.data]);
	const readyAgentId = cloudAgents.find((agent) => agent.authentication.state === "authorized")?.id ?? "";
	const [workerAgent, setWorkerAgent] = useState(readyAgentId);
	const [orchestratorAgent, setOrchestratorAgent] = useState(readyAgentId);

	// A login made from Harness settings (opened by the nudge below) lands here
	// once the connections list refetches — fill the still-empty selects with it
	// rather than forcing the user to reopen this step.
	useEffect(() => {
		if (readyAgentId === "") return;
		setWorkerAgent((current) => (current === "" ? readyAgentId : current));
		setOrchestratorAgent((current) => (current === "" ? readyAgentId : current));
	}, [readyAgentId]);

	const anyAgentReady = cloudAgents.some((agent) => agent.authentication.state === "authorized");
	const canCreate = !isCreating && workerAgent !== "" && orchestratorAgent !== "" && !templateMissing && !createBlocked;

	return (
		<div className="flex flex-col gap-5">
			{createError ? (
				<div className={onboardingAlertErrorClass} role="alert">
					{createError}
				</div>
			) : null}
			{/* Only harnesses connected for cloud are offered; the menu links to
			    Harness settings' Cloud view to connect more. */}
			<div className="grid grid-cols-2 gap-3">
				<RequiredAgentField
					id="cloudWorkerAgent"
					manageView="cloud"
					label={t("createProject.workerAgent", { defaultValue: "Worker" })}
					placeholder={t("createProject.chooseAgent", { defaultValue: "Choose an agent" })}
					agents={cloudAgents}
					value={workerAgent}
					onChange={setWorkerAgent}
				/>
				<RequiredAgentField
					id="cloudOrchestratorAgent"
					manageView="cloud"
					label={t("createProject.orchestratorAgent", { defaultValue: "Orchestrator" })}
					placeholder={t("createProject.chooseAgent", { defaultValue: "Choose an agent" })}
					agents={cloudAgents}
					value={orchestratorAgent}
					onChange={setOrchestratorAgent}
				/>
			</div>
			{/* Harness login lives only in Harness settings; project creation just
			    points there when no cloud harness is logged in yet. */}
			{connections.isSuccess && !anyAgentReady ? (
				<div className="flex items-center justify-between gap-3 rounded-lg border border-border/50 bg-[var(--color-bg-import-card)] px-4 py-3">
					<p className={onboardingFieldHintClass}>
						{t("createProject.cloudHarnessLoginNeeded", {
							defaultValue: "No harness is logged in for cloud yet. Log in to Claude Code, Codex, Cursor, or OpenCode to run this project's sessions.",
						})}
					</p>
					<Button
						type="button"
						variant="secondary"
						size="sm"
						className="shrink-0"
						onClick={() => openGlobalSettings("harness", { harnessView: "cloud", preserveProject: true })}
					>
						{t("createProject.openHarnessSettings", { defaultValue: "Go to Harness settings" })}
					</Button>
				</div>
			) : null}
			{templateMissing ? (
				<p className="text-[12px] leading-5 text-muted-foreground" role="alert">
					{t("createProject.coderTemplateRequiredHint", { defaultValue: "Select a Coder template above before creating this project." })}
				</p>
			) : null}
			<div className={onboardingFooterActionsClass}>
				<Button type="button" variant="secondary" onClick={onBack} disabled={isCreating}>
					{t("createProject.back", { defaultValue: "Back" })}
				</Button>
				<Button
					type="button"
					variant="primary"
					disabled={!canCreate}
					onClick={() => onCreate({ workerAgent, orchestratorAgent })}
				>
					{isCreating ? t("createProject.creating") : t("createProject.create")}
				</Button>
			</div>
		</div>
	);
}

/**
 * The org's GitHub App connection, which a cloud project's repositories come
 * from: its installations (polled while they sync), the repositories they
 * grant, and the secure connect flow. The control plane builds the
 * install/authorize URL (it holds the client id and secret), the browser
 * completes it against the CP's own callback, and this polls the CP until the
 * installation is recorded, then asks it to enumerate the granted
 * repositories before they are read. No GitHub credential ever touches the
 * desktop.
 */
export function useCloudGitHubAppConnection(onAuthRequired: () => void) {
	const { t } = useTranslation();
	const { client, baseUrl } = useCloudCp();
	const { org } = useCloudOrg();
	const queryClient = useQueryClient();
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const connectAbortRef = useRef<AbortController | null>(null);
	useEffect(() => () => connectAbortRef.current?.abort(), []);

	const installationsQuery = useQuery({
		queryKey: ["cloud-github-installations", baseUrl, org?.id],
		enabled: org !== undefined,
		staleTime: 0,
		refetchOnMount: "always",
		refetchInterval: (query) => {
			const installations = query.state.data ?? [];
			const syncing = installations.some(
				(installation) => installation.status === "active" && installation.syncStatus !== "ready",
			);
			if (syncing) return 2500;
			// GitHub may have removed an installation while this step stays open.
			return installations.some((installation) => installation.status === "active") ? 15_000 : false;
		},
		queryFn: async () => {
			if (!org) return [];
			const { installations } = await client.listGitHubInstallations(org.id);
			return installations;
		},
	});
	const activeInstallations = installationsQuery.data?.filter((installation) => installation.status === "active") ?? [];
	const installationNeedingSync =
		activeInstallations.find((installation) => installation.syncStatus === "retry" || installation.syncStatus === "failed") ??
		activeInstallations.find((installation) => installation.syncStatus !== "ready");
	const appConnected = activeInstallations.length > 0;
	const appRepositoriesReady = activeInstallations.some((installation) => installation.syncStatus === "ready");
	const allActiveInstallationsReady = appConnected && installationNeedingSync === undefined;
	const appRepositorySyncFailed = activeInstallations.some(
		(installation) => installation.syncStatus === "retry" || installation.syncStatus === "failed",
	);
	const readyInstallationKey = activeInstallations
		.filter((installation) => installation.syncStatus === "ready")
		.map((installation) => installation.id)
		.sort()
		.join(":");

	const repositories = useCloudGitHubAppRepositories(org?.id, appRepositoriesReady);
	useEffect(() => {
		if (readyInstallationKey === "") return;
		void queryClient.invalidateQueries({ queryKey: ["cloud-github-app-repos", baseUrl, org?.id] });
	}, [baseUrl, org?.id, queryClient, readyInstallationKey]);
	// GitHub is connected but grants no repositories yet: same next step as a
	// fresh start, so show the same "Connect repository" card.
	const noRepositoriesShared = appConnected && allActiveInstallationsReady && repositories.isSuccess && repositories.data.length === 0;

	const connect = async () => {
		if (busy) return;
		if (!org) {
			onAuthRequired();
			return;
		}
		const abort = new AbortController();
		connectAbortRef.current?.abort();
		connectAbortRef.current = abort;
		setBusy(true);
		setError(null);
		try {
			const { installations: installationsBeforeConnect } = await client.listGitHubInstallations(org.id, { signal: abort.signal });
			const previousUpdatedAtByID = new Map(
				installationsBeforeConnect.map((installation) => [installation.id, installation.updatedAt]),
			);
			const { installationUrl } = await client.startGitHubInstallation(org.id, { signal: abort.signal });
			await aoBridge.app.openExternal(installationUrl);
			// Poll for the installation the CP records once the user finishes in the
			// browser. Bounded so a browser left open does not spin forever.
			const deadline = Date.now() + 5 * 60_000;
			let connectedInstallation;
			for (;;) {
				await sleepWithAbort(2500, abort.signal);
				const { installations } = await client.listGitHubInstallations(org.id, { signal: abort.signal });
				connectedInstallation = installations.find(
					(installation) =>
						installation.status === "active" &&
						previousUpdatedAtByID.get(installation.id) !== installation.updatedAt,
				);
				if (connectedInstallation !== undefined) break;
				if (Date.now() > deadline) {
					setError(
						t("createProject.githubConnectTimeout", {
							defaultValue: "GitHub did not finish connecting. Try again.",
						}),
					);
					return;
				}
			}
			// Always sync before reading repositories. A new installation's grants
			// may still be syncing, and an existing installation that just gained
			// repositories ("Connect more repositories") is already marked ready
			// while GitHub's webhook for the new grant can land seconds later. The
			// sync endpoint enumerates the grants from GitHub before it returns, so
			// the repository query below sees exactly what was just granted.
			await client.syncGitHubInstallation(org.id, connectedInstallation.id, { signal: abort.signal });
		} catch (err) {
			if (abort.signal.aborted) return;
			// If the AO Cloud session lapsed mid-connect, the control plane returns
			// a 401 from its auth middleware. Re-authenticate rather than reporting
			// it as a GitHub failure.
			if (err instanceof CloudCpError && err.status === 401) {
				setError(
					t("createProject.cloudSessionExpiredForConnect", {
						defaultValue: "Your Cloud session expired. Sign in again, then connect GitHub.",
					}),
				);
				onAuthRequired();
			} else {
				setError(err instanceof Error ? err.message : t("createProject.githubAuthFailed", { defaultValue: "GitHub authentication failed" }));
			}
		} finally {
			if (!abort.signal.aborted) {
				await Promise.allSettled([
					queryClient.invalidateQueries({ queryKey: ["cloud-github-installations"] }),
					queryClient.invalidateQueries({ queryKey: ["cloud-github-app-repos"] }),
				]);
			}
			if (connectAbortRef.current === abort) connectAbortRef.current = null;
			setBusy(false);
		}
	};

	const retrySync = async () => {
		if (!org || !installationNeedingSync || busy) return;
		setBusy(true);
		setError(null);
		try {
			await client.syncGitHubInstallation(org.id, installationNeedingSync.id);
		} catch (err) {
			if (err instanceof CloudCpError && err.status === 401) onAuthRequired();
			setError(err instanceof Error ? err.message : t("createProject.githubReposFailed", { defaultValue: "Failed to load repositories." }));
		} finally {
			await Promise.allSettled([
				queryClient.invalidateQueries({ queryKey: ["cloud-github-installations"] }),
				queryClient.invalidateQueries({ queryKey: ["cloud-github-app-repos"] }),
			]);
			setBusy(false);
		}
	};

	const cancelConnect = () => {
		connectAbortRef.current?.abort();
		connectAbortRef.current = null;
		setBusy(false);
	};

	return {
		activeInstallations,
		appConnected,
		appRepositoriesReady,
		allActiveInstallationsReady,
		appRepositorySyncFailed,
		noRepositoriesShared,
		repositories,
		busy,
		error,
		connect,
		retrySync,
		cancelConnect,
	};
}

export type CloudGitHubAppConnection = ReturnType<typeof useCloudGitHubAppConnection>;

/** The "Connect repository" card shown until the org's GitHub App grants a repository. */
export function CloudGitHubConnectCard({ connection, disabled }: { connection: CloudGitHubAppConnection; disabled: boolean }) {
	const { t } = useTranslation();
	return (
		<Pressable
			className="flex w-full items-center gap-3 rounded-lg border border-border/50 bg-[var(--color-bg-import-card)] px-4 py-3 transition-colors hover:bg-accent/50 disabled:opacity-60"
			disabled={disabled || connection.busy}
			onClick={() => void connection.connect()}
		>
			<span className="grid size-9 shrink-0 place-items-center text-muted-foreground">
				<GitHubIcon className="size-5" />
			</span>
			<span className="min-w-0">
				<span className="block text-[14px] font-medium text-foreground">
					{connection.busy
						? t("createProject.githubWaiting", { defaultValue: "Waiting for GitHub..." })
						: t("createProject.connectRepository", { defaultValue: "Connect repository" })}
				</span>
				<span className="mt-0.5 block text-[12px] leading-5 text-muted-foreground">
					{connection.busy
						? t("createProject.githubCompleteInBrowser", { defaultValue: "Install the app on your repositories in the browser, then return here." })
						: t("createProject.connectRepositoryHint", { defaultValue: "Choose the GitHub repositories Orchestrator can work on" })}
				</span>
			</span>
			{connection.busy && (
				<span className="ml-auto size-4 animate-spin rounded-full border-2 border-muted-foreground border-t-transparent" />
			)}
		</Pressable>
	);
}

/** While GitHub is open in the browser, a way out; then the repositories' sync state and errors. */
export function CloudGitHubConnectionStatus({ connection }: { connection: CloudGitHubAppConnection }) {
	const { t } = useTranslation();
	const { appConnected, appRepositoriesReady, allActiveInstallationsReady, appRepositorySyncFailed, noRepositoriesShared, repositories, busy, error } = connection;
	return (
		<>
			{busy ? (
				<div className="flex items-center justify-between gap-3 text-[12px] leading-5 text-muted-foreground">
					<span>
						{appConnected
							? t("createProject.githubCompleteInBrowser", { defaultValue: "Install the app on your repositories in the browser, then return here." })
							: null}
					</span>
					<Button
						type="button"
						variant="quiet"
						size="xs"
						className="-mr-2 font-medium"
						onClick={connection.cancelConnect}
					>
						{t("createProject.cancel", { defaultValue: "Cancel" })}
					</Button>
				</div>
			) : null}

			{appConnected ? (
				repositories.isError ? (
					<div className="flex items-center gap-2 text-[12px] leading-5 text-destructive" role="alert">
						<span>{t("createProject.githubReposFailed", { defaultValue: "Failed to load repositories." })}</span>
						<Button type="button" variant="link" disabled={repositories.isFetching} onClick={() => void repositories.refetch()}>
							{t("createProject.retry")}
						</Button>
					</div>
				) : !appRepositoriesReady || (!allActiveInstallationsReady && !repositories.isLoading && (repositories.data?.length ?? 0) === 0) ? (
					<div className="flex items-center gap-2 text-[12px] leading-5 text-muted-foreground">
						<span>
							{appRepositorySyncFailed
								? t("createProject.githubReposFailed", { defaultValue: "Failed to load repositories." })
								: t("createProject.githubReposLoading", { defaultValue: "Loading repositories..." })}
						</span>
						{error || appRepositorySyncFailed ? (
							<Button type="button" variant="link" disabled={busy} onClick={() => void connection.retrySync()}>
								{t("createProject.retry")}
							</Button>
						) : null}
					</div>
				) : noRepositoriesShared ? null : appRepositorySyncFailed ? (
					<div className="flex items-center gap-2 text-[12px] leading-5 text-destructive">
						<span>{t("createProject.githubReposFailed", { defaultValue: "Failed to load repositories." })}</span>
						<Button type="button" variant="link" disabled={busy} onClick={() => void connection.retrySync()}>
							{t("createProject.retry")}
						</Button>
					</div>
				) : null
			) : null}
			{error ? (
				<p className="text-[12px] leading-5 text-destructive" role="alert">{error}</p>
			) : null}
		</>
	);
}

/**
 * The Coder options a new project carries (template, its parameters, and for
 * an org that brought its own Coder, the workspace name prefix), kept in the
 * coder session options store and reset with the card.
 */
export function useCloudCoderSetup(orgId: string | undefined, usesCoder: boolean, hasCoder: boolean) {
	// A coder project must pick a concrete template before it is created. With no
	// template, session start fails later with a 422 (coder_template_required) —
	// there is no implicit org-default for a bring-your-own-Coder deployment. So
	// require a selection whenever coder is active and the org has selectable
	// templates, and block create until one is chosen.
	const coderTemplates = useCoderTemplates(orgId, usesCoder);
	const templateId = useCoderSessionOptionsStore((s) => s.templateId);
	const templateMissing = usesCoder && coderTemplates.templates.length > 0 && templateId.trim() === "";
	const workspaceNamePrefix = useCoderSessionOptionsStore((s) => s.workspaceNamePrefix);
	// Workspace naming is a bring-your-own-Coder control: only an org that
	// connected its own Coder deployment sees and sends the prefix.
	const orgOwnsCoder = useOrgCoderConfig(hasCoder).data != null;
	const showWorkspaceNamePrefix = usesCoder && orgOwnsCoder;
	const workspaceNamePrefixInvalid = showWorkspaceNamePrefix && !isValidCoderWorkspaceNamePrefix(workspaceNamePrefix.trim());
	const resetCoderOptions = useCoderSessionOptionsStore((s) => s.reset);
	useEffect(() => {
		resetCoderOptions();
		return () => resetCoderOptions();
	}, [resetCoderOptions]);
	/** The project's config.coder, or undefined when its sessions do not run on Coder. */
	const coderConfig = () => {
		if (!usesCoder) return undefined;
		const options = useCoderSessionOptionsStore.getState();
		return buildCoderRequestOptions({ ...options, workspaceNamePrefix: showWorkspaceNamePrefix ? options.workspaceNamePrefix : "" });
	};
	return { orgId, usesCoder, templateId, templateMissing, showWorkspaceNamePrefix, workspaceNamePrefixInvalid, coderConfig };
}

export type CloudCoderSetup = ReturnType<typeof useCloudCoderSetup>;

/** Coder template and size are inherited by every session. */
export function CloudCoderSetupFields({ setup, disabled, projectName, repositoryUrls }: {
	setup: CloudCoderSetup;
	disabled: boolean;
	/** Names the default workspace prefix. */
	projectName: string;
	repositoryUrls: string[];
}) {
	const { t } = useTranslation();
	const parameters = useCoderSessionOptionsStore((s) => s.parameters);
	const setParameters = useCoderSessionOptionsStore((s) => s.setParameters);
	const workspaceNamePrefix = useCoderSessionOptionsStore((s) => s.workspaceNamePrefix);
	const setWorkspaceNamePrefix = useCoderSessionOptionsStore((s) => s.setWorkspaceNamePrefix);
	return (
		<div className="space-y-4">
			<CoderTemplatePicker orgId={setup.orgId} />
			{setup.templateId.trim() !== "" ? (
				<CoderTemplateOptions
					orgId={setup.orgId}
					templateId={setup.templateId}
					overrides={parameters}
					onChange={setParameters}
					disabled={disabled}
					repositoryUrls={repositoryUrls}
				/>
			) : null}
			{setup.showWorkspaceNamePrefix ? <div className="flex flex-col gap-2 text-sm">
				<Label htmlFor="coderWorkspaceNamePrefix" className="font-medium text-foreground">
					{t("coder.workspacePrefix.label")}
				</Label>
				<Input
					id="coderWorkspaceNamePrefix"
					value={workspaceNamePrefix}
					onChange={(event) => setWorkspaceNamePrefix(event.target.value)}
					placeholder={coderWorkspaceNamePrefixFor(projectName)}
					maxLength={20}
					spellCheck={false}
					autoCapitalize="off"
					autoComplete="off"
					className="font-mono"
					aria-invalid={setup.workspaceNamePrefixInvalid || undefined}
					aria-describedby="coderWorkspaceNamePrefixHint"
				/>
				<p
					id="coderWorkspaceNamePrefixHint"
					className={setup.workspaceNamePrefixInvalid ? onboardingFieldErrorClass : onboardingFieldHintClass}
					role={setup.workspaceNamePrefixInvalid ? "alert" : undefined}
				>
					{setup.workspaceNamePrefixInvalid ? t("coder.workspacePrefix.invalid") : t("coder.workspacePrefix.hint")}
				</p>
			</div> : null}
		</div>
	);
}

/**
 * After the control plane created a project: it goes to the top of the
 * sidebar, the project list refetches, and a Freestyle project opens on its
 * orchestrator, already starting (a Coder one opens on its board).
 */
export function useFinishCloudProjectCreate() {
	const queryClient = useQueryClient();
	return async (project: CloudCpProject, offering: Pick<ResolvedCloudOffering, "defaultProvider">) => {
		useSidebarOrderStore.getState().moveProjectToTop(project.id);
		await queryClient.invalidateQueries({ queryKey: cloudProjectsQueryKey });
		if (projectSandboxProvider(project.config, offering) === FREESTYLE_PROVIDER) {
			useUiStore.getState().setAutoStartOrchestrator(project.id, true);
		}
	};
}

/**
 * Tells the card's container whether it is creating, so a dialog holding it
 * stays open (Escape, an outside click) until the work ends: a folder upload
 * runs for minutes and cannot be cancelled. Reports false when it unmounts.
 */
export function useReportBusy(busy: boolean, onBusyChange?: (busy: boolean) => void) {
	const report = useRef(onBusyChange);
	report.current = onBusyChange;
	useEffect(() => {
		report.current?.(busy);
	}, [busy]);
	useEffect(() => () => report.current?.(false), []);
}

/** False once the component unmounts, so work that outlives it stops instead of acting for it. */
export function useMountedRef() {
	const mounted = useRef(true);
	useEffect(() => {
		mounted.current = true;
		return () => {
			mounted.current = false;
		};
	}, []);
	return mounted;
}

/** What a new cloud project is: one main repository (and more beside it), or a workspace. */
export type CloudCreateKind = "project" | "workspace";

/** Switches the cloud creation card between a project and a workspace. */
export function CloudCreateKindTabs({ value, onChange, disabled }: { value: CloudCreateKind; onChange: (kind: CloudCreateKind) => void; disabled?: boolean }) {
	const { t } = useTranslation();
	return (
		<Tabs value={value} onValueChange={(next) => onChange(next as CloudCreateKind)}>
			<TabsList aria-label={t("createProject.cloudKind", { defaultValue: "What to create" })}>
				<TabsTrigger value="project" disabled={disabled}>{t("createProject.cloudKindProject", { defaultValue: "Project" })}</TabsTrigger>
				<TabsTrigger value="workspace" disabled={disabled}>{t("createProject.cloudKindWorkspace", { defaultValue: "Workspace" })}</TabsTrigger>
			</TabsList>
		</Tabs>
	);
}
