import { Bot, KeyRound, Loader2, MonitorCog, Play, TriangleAlert, X, type LucideIcon } from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import * as Dialog from "@radix-ui/react-dialog";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useCloudGate } from "../hooks/useCloudGate";
import { useCloudSession } from "../lib/cloud-session";
import { ensureCodexAccounts } from "../hooks/useCodexAccountsQuery";
import { writeCodexAccounts } from "../hooks/codex-accounts-state";
import { GlobalSettingsForm } from "./GlobalSettingsForm";
import { ProjectSettingsForm, type ProjectSettingsSaveState, type ProjectSettingsSection as ProjectFormSection } from "./ProjectSettingsForm";
import { ProjectEnvironmentSettings } from "./ProjectEnvironmentSettings";
import { useCloudProjectsQuery, workspaceQueryOptions } from "../hooks/useWorkspaceQuery";
import { CuesSettings } from "./CuesDialog";
import { DialogHeader, settingsDialogBodyClass, settingsDialogHeaderClass, settingsDialogSurfaceClass } from "./ui/dialog";
import { type GlobalSettingsSection, type ProjectSettingsSection, type SettingsModal, useUiStore } from "../stores/ui-store";
import { cn } from "../lib/utils";
import { NAV_ROW_HIGHLIGHT_HOST_CLASS, NavRowHighlight } from "./NavRowHighlight";
import { labelForHost } from "../lib/host-clients";
import { LOCAL_HOST, refKey } from "../lib/hosts";
import { globalSettingsItem, visibleGlobalSettings } from "./settings/settingsCatalog";

// Internal testers who see the Coder (bring-your-own) settings page in addition
// to @11x.ai users, so the flow can be exercised on non-11x accounts.
const CODER_PAGE_TEST_EMAILS = new Set([
	"prateekkarnal77@gmail.com",
	"pritommazumdar1995@gmail.com",
	"c.mohak2004@gmail.com",
]);

function initialProjectSaveState(): ProjectSettingsSaveState {
	return { phase: "idle" };
}

export function SettingsDialog() {
	const settingsModal = useUiStore((state) => state.settingsModal);
	const projectSettings = settingsModal?.scope === "project" ? settingsModal : settingsModal?.returnTo;
	return (
		<>
			{projectSettings && <SettingsDialogLayer key={refKey({ host: projectSettings.hostId ?? LOCAL_HOST, id: projectSettings.projectId })} settingsModal={projectSettings} />}
			{settingsModal?.scope === "global" && <SettingsDialogLayer key="global" settingsModal={settingsModal} />}
		</>
	);
}

function SettingsDialogLayer({ settingsModal }: { settingsModal: SettingsModal }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const closeSettings = useUiStore((state) => state.closeSettings);
	const developerMode = useUiStore((state) => state.developerMode);
	// Diagnostics (memory and CPU) is listed only with its toggle on in Developer mode.
	const diagnostics = useUiStore((state) => state.developerMode && state.diagnostics);
	// Reads the daemon settings the dialog tree already queries; no extra fetch.
	const { cloudEnabled } = useCloudGate();
	// The bring-your-own-Coder page is for @11x.ai users, plus a small allowlist
	// of internal testers so the flow can be exercised on non-11x accounts.
	const email = (useCloudSession().session?.user.email ?? "").toLowerCase();
	const is11x = email.endsWith("@11x.ai") || CODER_PAGE_TEST_EMAILS.has(email);

	const displaySettings = settingsModal;
	// The selected page includes several store/query subscribers. Mount it one
	// frame after the lightweight dialog chrome so the opening interaction can
	// paint first.
	const deferSettingsBody = settingsModal.scope === "global";
	const [bodySettings, setBodySettings] = useState<SettingsModal | null>(() =>
		deferSettingsBody ? null : settingsModal,
	);
	useEffect(() => {
		if (settingsModal === null) return;
		if (!deferSettingsBody) {
			setBodySettings(settingsModal);
			return;
		}
		const frame = requestAnimationFrame(() => setBodySettings(settingsModal));
		return () => cancelAnimationFrame(frame);
	}, [deferSettingsBody, settingsModal]);
	const isBodyReady = bodySettings === displaySettings;

	const globalSections = visibleGlobalSettings({ cloudEnabled, developerMode, diagnostics, is11x });
	const remoteHostId = displaySettings?.scope === "project" ? displaySettings.hostId : undefined;
	// A cloud project lives only in the control plane; the local daemon has no
	// record of it. Resolve it here so its settings load from the control plane.
	const explicitCloudOrgId = displaySettings?.scope === "project" ? displaySettings.cloudOrgId : undefined;
	const localProjectScope = displaySettings?.scope === "project" && !remoteHostId && explicitCloudOrgId === undefined;
	const cloudProjects = useCloudProjectsQuery({ enabled: localProjectScope });
	const cloudProject = localProjectScope
		? cloudProjects.data?.find((project) => project.id === displaySettings.projectId)
		: undefined;
	// Only fall back to the local daemon's form for a project the local daemon
	// actually lists: a failed cloud lookup must not masquerade as a local
	// project (the daemon would answer "Unknown project" for a cloud id).
	const projectId = displaySettings?.scope === "project" ? displaySettings.projectId : "";
	const knownLocal = useQuery({
		...workspaceQueryOptions,
		enabled: localProjectScope,
		select: (workspaces) => workspaces.some((workspace) => workspace.id === projectId),
	}).data === true;
	const cloudProjectsPending = localProjectScope && !knownLocal && cloudProjects.isLoading;
	const cloudLookupFailed = localProjectScope && !cloudProject && !knownLocal && cloudProjects.isError;

	const cloudOrgId = explicitCloudOrgId ?? cloudProject?.orgId;
	const isCloudProjectSettings = displaySettings?.scope === "project" && cloudOrgId !== undefined;
	const projectSections: Array<{
		id: ProjectSettingsSection;
		label: string;
		icon: LucideIcon;
	}> = [
		{ id: "general", label: t("settings.project.general"), icon: MonitorCog },
		{ id: "agents", label: t("settings.project.agents"), icon: Bot },
	];
	// Environment and cues are local-daemon features; remote hosts and Cloud projects do not expose them.
	if (!remoteHostId && !isCloudProjectSettings) {
		projectSections.push({ id: "environment", label: t("settings.project.environment"), icon: KeyRound });
		projectSections.push({ id: "cues", label: t("cues.title"), icon: Play });
	}

	const isProjectSettings = displaySettings?.scope === "project";
	const [activeSection, setActiveSection] = useState<GlobalSettingsSection>("general");
	const [focusAgentId, setFocusAgentId] = useState<string>();
	const [harnessView, setHarnessView] = useState<"local" | "cloud">();
	const [activeProjectSection, setActiveProjectSection] = useState<ProjectSettingsSection>("general");
	const [pendingProjectSection, setPendingProjectSection] = useState<ProjectSettingsSection | null>(null);
	const [projectSaveState, setProjectSaveState] = useState<ProjectSettingsSaveState>(initialProjectSaveState);
	const [cueBusy, setCueBusy] = useState(false);
	useEffect(() => {
		if (pendingProjectSection && projectSaveState.phase === "saved" && !projectSaveState.dirty) {
			setActiveProjectSection(pendingProjectSection);
			setPendingProjectSection(null);
		}
	}, [pendingProjectSection, projectSaveState]);
	const closeWhenSavedRef = useRef(false);
	const globalSettingsWasOpen = useRef(false);

	const activeLabel = isProjectSettings
		? (projectSections.find((s) => s.id === activeProjectSection)?.label ?? t("settings.project.general"))
		: globalSettingsItem(activeSection, { cloudEnabled, developerMode, diagnostics, is11x }).label(t);

	const closeSettingsDialog = () => {
		if (cueBusy) return;
		if (isProjectSettings) {
			if (closeWhenSavedRef.current) return;
			if (projectSaveState.requestPending) {
				closeWhenSavedRef.current = true;
				return;
			}
			if (projectSaveState.dirty) {
				const form = document.getElementById("project-settings-form") as HTMLFormElement | null;
				if (form) {
					closeWhenSavedRef.current = true;
					form.requestSubmit();
					return;
				}
			}
			if (projectSaveState.phase === "pending" || projectSaveState.phase === "saving") {
				closeWhenSavedRef.current = true;
				return;
			}
		}
		closeSettings();
	};
	useEffect(() => {
		if (!closeWhenSavedRef.current) return;
		if (projectSaveState.phase === "failed" || projectSaveState.replacementError) {
			closeWhenSavedRef.current = false;
		} else if (!projectSaveState.dirty && !projectSaveState.requestPending &&
			(projectSaveState.phase === "saved" || projectSaveState.phase === "idle")) {
			closeWhenSavedRef.current = false;
			closeSettings();
		}
	}, [closeSettings, projectSaveState]);
	const closeButtonRef = useRef<HTMLButtonElement>(null);
	const contentRef = useRef<HTMLDivElement>(null);
	const returnFocusRef = useRef(document.activeElement as HTMLElement | null);
	const returnDialogRef = useRef(returnFocusRef.current?.closest<HTMLElement>('[role="dialog"]') ?? null);
	const hasAgentFocusTarget = settingsModal.scope === "global" && Boolean(settingsModal.focusAgentId);
	useEffect(() => {
		if (hasAgentFocusTarget) return;
		// The modal contains focus immediately. Move visible focus after the
		// first paint because focus() forces style resolution.
		let focusTimer = 0;
		const focusFrame = requestAnimationFrame(() => {
			focusTimer = window.setTimeout(() => closeButtonRef.current?.focus({ preventScroll: true }), 0);
		});
		return () => {
			cancelAnimationFrame(focusFrame);
			window.clearTimeout(focusTimer);
		};
	}, [hasAgentFocusTarget]);

	useEffect(() => {
		if (settingsModal?.scope === "global") {
			setActiveSection(globalSettingsItem(settingsModal.section ?? "general", { cloudEnabled, developerMode, diagnostics, is11x }).id);
		}
		if (settingsModal?.scope === "project") {
			setActiveProjectSection(settingsModal.cloudOrgId !== undefined && settingsModal.section === "cues" ? "general" : settingsModal.section ?? "general");
			setProjectSaveState(initialProjectSaveState());
			setCueBusy(false);
		}
	}, [cloudEnabled, developerMode, diagnostics, is11x, settingsModal]);

	useEffect(() => {
		setFocusAgentId(settingsModal?.scope === "global" ? settingsModal.focusAgentId : undefined);
		setHarnessView(settingsModal?.scope === "global" ? settingsModal.harnessView : undefined);
	}, [settingsModal]);

	useEffect(() => {
		const globalSettingsOpen = settingsModal?.scope === "global";
		if (!globalSettingsOpen) {
			globalSettingsWasOpen.current = false;
			return;
		}
		if (globalSettingsWasOpen.current) return;
		globalSettingsWasOpen.current = true;
		// Warm account management as soon as global Settings opens, regardless of
		// which page is selected. By the time the user visits Accounts, external
		// login/logout changes and saved-account observations are already current.
		void ensureCodexAccounts([], {
			includeUsage: true,
			forceAuthentication: true,
			forceDeviceReconciliation: true,
		})
			.then((next) => writeCodexAccounts(queryClient, next, "replace"))
			.catch(() => undefined);
	}, [queryClient, settingsModal?.scope]);

	return (
		<Dialog.Root
			open
			onOpenChange={(open) => {
				if (!open) closeSettingsDialog();
			}}
		>
			<Dialog.Portal>
				<Dialog.Overlay
					className="dialog-overlay z-[calc(var(--z-overlay)-1)] animate-overlay-in motion-reduce:animate-none"
					data-testid="settings-dialog-overlay"
					onWheel={(event) => event.preventDefault()}
				/>
				<Dialog.Content
					aria-modal="true"
					className={cn(
						settingsDialogSurfaceClass,
						"fixed left-1/2 top-1/2 z-overlay h-(--size-settings-dialog-height) w-(--size-settings-dialog-wide) max-h-none -translate-x-1/2 -translate-y-1/2 origin-center overflow-hidden p-0 animate-modal-in motion-reduce:animate-none sm:rounded-lg",
						isProjectSettings && "h-[min(40rem,calc(100vh-3rem))]",
					)}
					onOpenAutoFocus={(event) => event.preventDefault()}
					onEscapeKeyDown={(event) => {
						const target = event.target instanceof Element ? event.target : null;
						// An in-place edit (a profile rename) takes Escape to cancel itself,
						// not to close Settings around it.
						if (target?.closest("[data-settings-inline-edit]")) {
							event.preventDefault();
							return;
						}
						if (contentRef.current?.contains(event.target as Node)) return;
						const activeElement = document.activeElement instanceof Element ? document.activeElement : null;
						const nestedPopup = [target, activeElement].some((element) => element?.closest('[role="menu"], [role="listbox"], [data-radix-popper-content-wrapper]'));
						if (nestedPopup) event.preventDefault();
					}}
					onCloseAutoFocus={(event) => {
						event.preventDefault();
						// Successful recovery removes its CTA. The originating dialog
						// remains mounted and can receive focus when that happens.
						const target = returnFocusRef.current?.isConnected ? returnFocusRef.current : returnDialogRef.current;
						if (target?.isConnected) target.focus({ preventScroll: true });
					}}
					ref={contentRef}
				>
					<div className="flex h-full min-h-0">
						<aside className="flex w-48 shrink-0 flex-col border-r border-(--color-border-settings-dialog-header) bg-card">
							<p className="px-3 pb-1 pt-1.5 text-2xs font-medium tracking-normal text-muted-foreground/60">{t("settings.title")}</p>
							<nav aria-label={t("settings.navSectionsAria")} className="flex flex-col gap-0.5 p-2 pt-0">
								{isProjectSettings
									? projectSections.map(({ id, label, icon }) => (
											<SettingsNavItem active={activeProjectSection === id} disabled={cueBusy} icon={icon} key={id} label={label} onClick={() => {
												if (projectSaveState.dirty && id !== activeProjectSection) {
													setPendingProjectSection(id);
													(document.getElementById("project-settings-form") as HTMLFormElement | null)?.requestSubmit();
												} else {
													setActiveProjectSection(id);
												}
											}} />
										))
									: globalSections.map(({ id, label, icon }) => (
											<SettingsNavItem
												active={activeSection === id}
												icon={icon}
												key={id}
												label={label(t)}
												onClick={() => {
													setActiveSection(id);
													setFocusAgentId(undefined);
												}}
											/>
										))}
							</nav>
							{isProjectSettings && activeProjectSection !== "cues" &&
								(projectSaveState.phase === "failed" ||
									projectSaveState.phase === "pending" ||
									projectSaveState.phase === "saving" ||
									(remoteHostId && projectSaveState.replacementError)) && (
								<div className="mt-auto border-t border-(--color-border-settings-dialog-header) px-4 py-3 text-xs" role="status" aria-live="polite">
									{projectSaveState.phase === "failed" || (remoteHostId && projectSaveState.replacementError) ? (
										<div className="space-y-2 text-error">
											<p className="flex items-start gap-2" role="alert"><TriangleAlert className="size-4 shrink-0" aria-hidden="true" />{projectSaveState.error ?? projectSaveState.replacementError ?? t("settings.project.saveFailed")}</p>
											<button className="text-settings-label underline underline-offset-2" onClick={() => {
												if (projectSaveState.retry) projectSaveState.retry();
												else (document.getElementById("project-settings-form") as HTMLFormElement | null)?.requestSubmit();
											}} type="button">{t("createProject.retry")}</button>
										</div>
									) : (
										<p className="flex items-center gap-2 text-settings-muted">
											{projectSaveState.phase === "pending" && activeProjectSection === "environment" ? t("settings.project.unsavedChanges") : <><Loader2 className="size-4 shrink-0 animate-spin" aria-hidden="true" />{t("settings.project.saving")}</>}
										</p>
									)}
								</div>
							)}
						</aside>

						{/* Main area — same bg as the app page */}
						<div className="flex min-w-0 flex-1 flex-col bg-card">
							<DialogHeader className={cn(settingsDialogHeaderClass, "flex h-auto shrink-0 flex-row items-center justify-between border-b-0 px-(--size-modal-padding) py-3")}>
								<Dialog.Title className={cn(isProjectSettings ? "settings-dialog-title" : "text-2xl font-bold text-foreground")}>{activeLabel}{remoteHostId && <span className="ml-2 text-xs font-normal text-muted-foreground">· {labelForHost(remoteHostId) ?? remoteHostId}</span>}</Dialog.Title>
								<Dialog.Description className="sr-only">
									{isProjectSettings
										? t("settings.project.dialogDescription")
										: t("settings.dialogDescription", {
												section: activeLabel.toLowerCase(),
											})}
								</Dialog.Description>
								<button
									aria-label={t("settings.close")}
									className="settings-close-button"
									disabled={cueBusy}
									onClick={closeSettingsDialog}
									ref={closeButtonRef}
									type="button"
								>
									<X aria-hidden="true" className="size-4" />
								</button>
							</DialogHeader>
							<div aria-busy={!isBodyReady} className={cn(settingsDialogBodyClass, "settings-dialog-body flex-1 px-(--size-modal-padding) pt-0")}>
								{isBodyReady ? (
									cloudProjectsPending ? (
										<p className="text-sm text-settings-muted">{t("settings.project.loading")}</p>
									) : cloudLookupFailed ? (
										<div className="space-y-2 text-sm text-error" role="alert">
											<p>{t("settings.project.cloudLoadFailed")} {cloudProjects.error instanceof Error ? cloudProjects.error.message : ""}</p>
											<button className="text-settings-label underline underline-offset-2" onClick={() => void cloudProjects.refetch()} type="button">{t("settings.project.retry")}</button>
										</div>
									) : displaySettings?.scope === "project" && !remoteHostId && !isCloudProjectSettings && activeProjectSection === "cues" ? (
										<CuesSettings projectId={displaySettings.projectId} onBusyChange={setCueBusy} />
									) : displaySettings?.scope === "project" && !remoteHostId && !isCloudProjectSettings && activeProjectSection === "environment" ? (
										<ProjectEnvironmentSettings projectId={displaySettings.projectId} onSaveState={setProjectSaveState} />
									) : displaySettings?.scope === "project" ? (
										<ProjectSettingsForm projectId={displaySettings.projectId} hostId={remoteHostId} cloudOrgId={cloudOrgId} section={activeProjectSection as ProjectFormSection} onSaveState={setProjectSaveState} />
									) : (
										<GlobalSettingsForm cloudEnabled={cloudEnabled} is11x={is11x} focusAgentId={focusAgentId} hostId={displaySettings?.scope === "global" ? displaySettings.hostId : undefined} harnessView={harnessView} section={activeSection} />
									)
								) : (
									<div aria-hidden="true" className="h-full" data-testid="settings-dialog-body-pending" />
								)}
							</div>
						</div>
					</div>
				</Dialog.Content>
			</Dialog.Portal>
		</Dialog.Root>
	);
}

function SettingsNavItem({ active, disabled, icon: Icon, label, onClick }: { active: boolean; disabled?: boolean; icon: LucideIcon; label: string; onClick: () => void }) {
	return (
		<button
			aria-current={active ? "page" : undefined}
			className={cn(
				NAV_ROW_HIGHLIGHT_HOST_CLASS,
				"flex h-8 w-full items-center gap-2 rounded-lg px-2.5 text-left text-sm font-medium text-muted-foreground transition-none focus:outline-none focus-visible:outline-none focus-visible:ring-0 disabled:cursor-not-allowed disabled:opacity-50",
			)}
			data-active={active}
			disabled={disabled}
			onClick={onClick}
			type="button"
		>
			<NavRowHighlight active={active} disabled={disabled} />
			<Icon aria-hidden="true" className="relative z-[1] size-icon-md shrink-0" />
			<span className="relative z-[1] min-w-0 flex-1 truncate">{label}</span>
		</button>
	);
}
