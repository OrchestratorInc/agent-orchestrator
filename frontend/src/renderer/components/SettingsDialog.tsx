import { Bot, Coffee, FolderGit2, KeyRound, Loader2, MonitorCog, Play, ScrollText, TriangleAlert, Wrench, X, type LucideIcon } from "lucide-react";
import * as Dialog from "@radix-ui/react-dialog";
import { createPortal } from "react-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { aoBridge } from "../lib/bridge";
import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useCloudGate } from "../hooks/useCloudGate";
import { useCloudOffering } from "../hooks/useCloudOffering";
import { useRestoreFocusOnClose } from "../hooks/useRestoreFocusOnClose";
import { ensureCodexAccounts } from "../hooks/useCodexAccountsQuery";
import { writeCodexAccounts } from "../hooks/codex-accounts-state";
import { GlobalSettingsForm } from "./GlobalSettingsForm";
import { ProjectSettingsForm, type ProjectSettingsSaveState, type ProjectSettingsSection as ProjectFormSection } from "./ProjectSettingsForm";
import { CloudProjectScriptsSettings, ProjectScriptsSettings } from "./ProjectScriptsSettings";
import { CloudProjectInstructionsSettings, CloudProjectRepositoriesSettings } from "./CloudProjectLayoutSettings";
import { ProjectEnvironmentSettings } from "./ProjectEnvironmentSettings";
import { useCloudProjectsQuery, workspaceQueryOptions } from "../hooks/useWorkspaceQuery";
import { CuesSettings } from "./CuesDialog";
import { topbarDragStyle } from "./ShellTopbar";
import { useAppShortcut } from "../hooks/useAppShortcut";
import { type GlobalSettingsSection, type HarnessDiagnosticsRequest, type ProjectSettingsSection, type SettingsModal, useUiStore } from "../stores/ui-store";
import { cn } from "../lib/utils";
import { DialogHeader, settingsDialogBodyClass, settingsDialogHeaderClass, settingsDialogSurfaceClass } from "./ui/dialog";
import { NAV_ROW_HIGHLIGHT_HOST_CLASS, NavRowHighlight } from "./NavRowHighlight";
import { labelForHost } from "../lib/host-clients";
import { LOCAL_HOST, refKey } from "../lib/hosts";
import { globalSettingsItem, visibleGlobalSettings } from "./settings/settingsCatalog";
import { fetchMobileStatus, mobileStatusQueryKey } from "./settings/ConnectMobileContent";
import { fetchDevices, mobileDevicesQueryKey } from "./settings/MobileDevicesSection";

function initialProjectSaveState(): ProjectSettingsSaveState {
	return { phase: "idle" };
}

function useSettingsLayer(settingsModal: SettingsModal | null) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const closeSettings = useUiStore((state) => state.closeSettings);
	const developerMode = useUiStore((state) => state.developerMode);
	// Diagnostics (memory and CPU) is listed only with its toggle on in Developer mode.
	const diagnostics = useUiStore((state) => state.developerMode && state.diagnostics);
	// Reads the daemon settings the dialog tree already queries; no extra fetch.
	const { cloudEnabled } = useCloudGate();
	// The org's offering decides the cloud pages: the bring-your-own-Coder page
	// only when it includes coder, Upgrade only for self-serve billing.
	const { customCoder, selfServeBilling } = useCloudOffering();

	const displaySettings = settingsModal;
	// The selected page includes several store/query subscribers. Mount it one
	// frame after the lightweight dialog chrome so the opening interaction can
	// paint first.
	const deferSettingsBody = settingsModal?.scope === "global";
	const [bodySettings, setBodySettings] = useState<SettingsModal | null>(() =>
		deferSettingsBody ? null : settingsModal,
	);
	useEffect(() => {
		if (settingsModal === null) {
			setBodySettings(null);
			return;
		}
		if (!deferSettingsBody) {
			setBodySettings(settingsModal);
			return;
		}
		const frame = requestAnimationFrame(() => setBodySettings(settingsModal));
		return () => cancelAnimationFrame(frame);
	}, [deferSettingsBody, settingsModal]);
	const isBodyReady = bodySettings === displaySettings;

	const globalSections = visibleGlobalSettings({ cloudEnabled, developerMode, diagnostics, customCoder, selfServeBilling });
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
	// Scripts are project settings on local and Cloud projects alike (a Cloud
	// session's worker runs them). Environment and cues are local-daemon
	// features; remote hosts expose none of these.
	// A Cloud project's repositories, where its agent starts, and the
	// instructions added to its agents' prompts live on the control plane.
	if (isCloudProjectSettings) {
		projectSections.push({ id: "repositories", label: t("settings.project.repositories", { defaultValue: "Repositories" }), icon: FolderGit2 });
		projectSections.push({ id: "instructions", label: t("settings.project.instructions", { defaultValue: "Instructions" }), icon: ScrollText });
	}
	if (!remoteHostId) projectSections.push({ id: "scripts", label: t("settings.project.scripts"), icon: Wrench });
	if (!remoteHostId && !isCloudProjectSettings) {
		projectSections.push({ id: "environment", label: t("settings.project.environment"), icon: KeyRound });
		projectSections.push({ id: "cues", label: t("cues.title"), icon: Play });
	}

	const isProjectSettings = displaySettings?.scope === "project";
	const [activeSection, setActiveSection] = useState<GlobalSettingsSection>("general");
	const [focusAgentId, setFocusAgentId] = useState<string>();
	const [harnessView, setHarnessView] = useState<"local" | "cloud">();
	const [startLogin, setStartLogin] = useState(false);
	const [showDiagnostics, setShowDiagnostics] = useState<HarnessDiagnosticsRequest | undefined>();
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

	const activeLabel = !settingsModal ? "" : isProjectSettings
		? (projectSections.find((s) => s.id === activeProjectSection)?.label ?? t("settings.project.general"))
		: globalSettingsItem(activeSection, { cloudEnabled, developerMode, diagnostics, customCoder, selfServeBilling }).label(t);

	const closeSettingsDialog = () => {
		if (!settingsModal) return;
		if (cueBusy) return;
		if (isProjectSettings) {
			if (closeWhenSavedRef.current) return;
			if (projectSaveState.requestPending) {
				closeWhenSavedRef.current = true;
				return;
			}
			// A draft that cannot be saved is dropped; waiting for a save would hang the close.
			if (projectSaveState.unsaveable) {
				closeSettings();
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
		} else if (projectSaveState.unsaveable && !projectSaveState.requestPending) {
			closeWhenSavedRef.current = false;
			closeSettings();
		} else if (!projectSaveState.dirty && !projectSaveState.requestPending &&
			(projectSaveState.phase === "saved" || projectSaveState.phase === "idle")) {
			closeWhenSavedRef.current = false;
			closeSettings();
		}
	}, [closeSettings, projectSaveState]);
	// Re-resolves the requested page as the gates settle: the org's offering
	// loads after the dialog opens, so a deep link to Upgrade or Coder lands on
	// it once the offering allows it.
	useEffect(() => {
		if (settingsModal?.scope === "global") {
			setActiveSection(globalSettingsItem(settingsModal.section ?? "general", { cloudEnabled, developerMode, diagnostics, customCoder, selfServeBilling }).id);
		}
	}, [cloudEnabled, developerMode, diagnostics, customCoder, selfServeBilling, settingsModal]);
	// Project scope resets only when a project is opened, never when a gate
	// settles, so a reported save state survives the offering loading.
	useEffect(() => {
		if (settingsModal?.scope === "project") {
			setActiveProjectSection(settingsModal.cloudOrgId !== undefined && settingsModal.section === "cues" ? "general" : settingsModal.section ?? "general");
			setProjectSaveState(initialProjectSaveState());
			setCueBusy(false);
		}
	}, [settingsModal]);

	useEffect(() => {
		setFocusAgentId(settingsModal?.scope === "global" ? settingsModal.focusAgentId : undefined);
		setHarnessView(settingsModal?.scope === "global" ? settingsModal.harnessView : undefined);
		setStartLogin(settingsModal?.scope === "global" && settingsModal.startLogin === true);
		setShowDiagnostics(settingsModal?.scope === "global" ? settingsModal.showDiagnostics : undefined);
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
		// Same for Mobile: its pairing state and Keep awake are known before the
		// page is first drawn, so nothing pops in or flips after it opens.
		void queryClient.prefetchQuery({ queryKey: mobileStatusQueryKey, queryFn: fetchMobileStatus });
		void queryClient.prefetchQuery({ queryKey: mobileDevicesQueryKey, queryFn: fetchDevices });
	}, [queryClient, settingsModal?.scope]);

	const selectProjectSection = (id: ProjectSettingsSection) => {
		if (projectSaveState.dirty && !projectSaveState.unsaveable && id !== activeProjectSection) {
			setPendingProjectSection(id);
			(document.getElementById("project-settings-form") as HTMLFormElement | null)?.requestSubmit();
		} else {
			setActiveProjectSection(id);
		}
	};
	const selectGlobalSection = (id: GlobalSettingsSection) => {
		setActiveSection(id);
		setFocusAgentId(undefined);
	};
	const navItems: SettingsNavEntry[] = isProjectSettings
		? projectSections.map(({ id, label, icon }) => ({ id, label, icon, active: activeProjectSection === id, disabled: cueBusy, onSelect: () => selectProjectSection(id) }))
		: globalSections.map(({ id, label, icon }) => ({ id, label: label(t), icon, active: activeSection === id, onSelect: () => selectGlobalSection(id) }));
	const showSaveStatus = isProjectSettings && activeProjectSection !== "cues" &&
		(projectSaveState.phase === "failed" ||
			(projectSaveState.phase === "pending" && !projectSaveState.unsaveable) ||
			projectSaveState.phase === "saving" ||
			Boolean(remoteHostId && projectSaveState.replacementError));

	return {
		modal: settingsModal,
		navItems,
		showSaveStatus,
		projectSaveState,
		activeProjectSection,
		remoteHostId,
		cueBusy,
		close: closeSettingsDialog,
		title: activeLabel,
		titleIcon: !isProjectSettings && activeSection === "stayAwake",
		isBodyReady,
		bodyKey: displaySettings?.scope === "project" ? refKey({ host: displaySettings.hostId ?? LOCAL_HOST, id: displaySettings.projectId }) : "global",
		body: () => {
			if (!isBodyReady) return <div aria-hidden="true" className="h-full" data-testid="settings-dialog-body-pending" />;
			if (cloudProjectsPending) return <p className="text-sm text-settings-muted">{t("settings.project.loading")}</p>;
			if (cloudLookupFailed) {
				return (
					<div className="space-y-2 text-sm text-error" role="alert">
						<p>{t("settings.project.cloudLoadFailed")} {cloudProjects.error instanceof Error ? cloudProjects.error.message : ""}</p>
						<button className="text-settings-label underline underline-offset-2" onClick={() => void cloudProjects.refetch()} type="button">{t("settings.project.retry")}</button>
					</div>
				);
			}
			if (displaySettings?.scope === "project" && !remoteHostId && !isCloudProjectSettings && activeProjectSection === "cues") {
				return <CuesSettings projectId={displaySettings.projectId} onBusyChange={setCueBusy} />;
			}
			if (displaySettings?.scope === "project" && isCloudProjectSettings && cloudOrgId !== undefined && activeProjectSection === "repositories") {
				return <CloudProjectRepositoriesSettings projectId={displaySettings.projectId} cloudOrgId={cloudOrgId} onSaveState={setProjectSaveState} />;
			}
			if (displaySettings?.scope === "project" && isCloudProjectSettings && cloudOrgId !== undefined && activeProjectSection === "instructions") {
				return <CloudProjectInstructionsSettings projectId={displaySettings.projectId} cloudOrgId={cloudOrgId} onSaveState={setProjectSaveState} />;
			}
			if (displaySettings?.scope === "project" && !remoteHostId && activeProjectSection === "scripts") {
				return isCloudProjectSettings && cloudOrgId !== undefined
					? <CloudProjectScriptsSettings projectId={displaySettings.projectId} cloudOrgId={cloudOrgId} onSaveState={setProjectSaveState} />
					: <ProjectScriptsSettings projectId={displaySettings.projectId} onSaveState={setProjectSaveState} />;
			}
			if (displaySettings?.scope === "project" && !remoteHostId && !isCloudProjectSettings && activeProjectSection === "environment") {
				return <ProjectEnvironmentSettings projectId={displaySettings.projectId} onSaveState={setProjectSaveState} />;
			}
			if (displaySettings?.scope === "project") {
				return <ProjectSettingsForm projectId={displaySettings.projectId} hostId={remoteHostId} cloudOrgId={cloudOrgId} section={activeProjectSection as ProjectFormSection} onSaveState={setProjectSaveState} />;
			}
			return <GlobalSettingsForm cloudEnabled={cloudEnabled} customCoder={customCoder} selfServeBilling={selfServeBilling} focusAgentId={focusAgentId} hostId={displaySettings?.scope === "global" ? displaySettings.hostId : undefined} harnessView={harnessView} startLogin={startLogin} showDiagnostics={showDiagnostics} section={activeSection} />;
		},
	};
}

export type SettingsNavEntry = {
	id: string;
	label: string;
	icon: LucideIcon;
	active: boolean;
	disabled?: boolean;
	onSelect: () => void;
};

type SettingsLayer = ReturnType<typeof useSettingsLayer>;

type SettingsPageValue = { active: SettingsLayer; layers: SettingsLayer[] };

const SettingsPageContext = createContext<SettingsPageValue | null>(null);

/** The settings layer currently on top, or null while settings is closed. */
export function useSettingsPage() {
	return useContext(SettingsPageContext)?.active ?? null;
}

/**
 * Owns settings state for the shell so the sidebar (section list, Back) and the
 * center pane (page body) stay in sync. A recovery settings page opened above a
 * project form keeps the project layer's state alive underneath.
 */
export function SettingsProvider({ children }: { children: ReactNode }) {
	const settingsModal = useUiStore((state) => state.settingsModal);
	const projectModal = settingsModal?.scope === "project" ? settingsModal : settingsModal?.returnTo ?? null;
	const globalModal = settingsModal?.scope === "global" ? settingsModal : null;
	const projectLayer = useSettingsLayer(projectModal);
	const globalLayer = useSettingsLayer(globalModal);
	// Global settings is a page in the shell; project settings stays a modal.
	const active = globalModal ? globalLayer : null;
	const activeRef = useRef(active);
	activeRef.current = active;
	const isOpen = active !== null;
	// Hand focus back to whatever opened settings once the page closes.
	useEffect(() => {
		if (!isOpen) return;
		const opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
		return () => { if (opener?.isConnected) opener.focus({ preventScroll: true }); };
	}, [isOpen]);
	// Settings sections follow the app's navigation chords while the page is
	// open: previous/next (⌘⇧[ ⌘⇧], ⌃Tab, ⌘⌥←/→) step through the section list and
	// ⌘1-9 jumps to the Nth section. The shell stands down for these meanwhile.
	const stepSection = useCallback((direction: -1 | 1) => {
		const items = activeRef.current?.navItems.filter((item) => !item.disabled) ?? [];
		if (items.length === 0) return;
		const current = items.findIndex((item) => item.active);
		const next = items[(current + direction + items.length) % items.length];
		next?.onSelect();
	}, []);
	useEffect(() => {
		if (!isOpen) return;
		const disposers = [
			aoBridge.app.onPreviousSessionShortcut(() => stepSection(-1)),
			aoBridge.app.onNextSessionShortcut(() => stepSection(1)),
			aoBridge.app.onPreviousTabShortcut(() => stepSection(-1)),
			aoBridge.app.onNextTabShortcut(() => stepSection(1)),
		];
		return () => disposers.forEach((dispose) => dispose());
	}, [isOpen, stepSection]);
	useAppShortcut(
		"jump-to-session",
		({ index }) => {
			const items = activeRef.current?.navItems.filter((item) => !item.disabled) ?? [];
			if (index !== undefined) items[index]?.onSelect();
		},
		isOpen,
	);
	useEffect(() => {
		if (!isOpen) return;
		const onKeyDown = (event: KeyboardEvent) => {
			if (event.key !== "Escape" || event.isComposing) return;
			const target = event.target instanceof Element ? event.target : null;
			// In-place edits (a profile rename) and login flows (terminal, cloud login panel) take Escape themselves.
			if (target?.closest("[data-settings-inline-edit]")) return;
			// Open menus, listboxes, and dialogs take Escape to dismiss themselves. Only popups the
			// event came from (or that hold focus) count, so a stray tooltip cannot swallow it.
			if (event.isComposing) return;
			const focused = document.activeElement instanceof Element ? document.activeElement : null;
			const popup = '[role="menu"], [role="listbox"], [role="dialog"], [data-radix-popper-content-wrapper]';
			if ([target, focused].some((element) => element?.closest(popup))) return;
			activeRef.current?.close();
		};
		// Capture phase so a field that handles its own Escape cannot swallow the close.
		document.addEventListener("keydown", onKeyDown, true);
		return () => document.removeEventListener("keydown", onKeyDown, true);
	}, [isOpen]);
	return (
		<SettingsPageContext.Provider value={active ? { active, layers: [globalLayer] } : null}>
			{children}
			{projectModal && <ProjectSettingsModal key={projectLayer.bodyKey} layer={projectLayer} covered={globalModal !== null} />}
		</SettingsPageContext.Provider>
	);
}

/**
 * Settings page rendered inside the center panel, in place of the routed page:
 * no top bar, just one centered, scrolling content column on the normal page
 * background, headed by the page's sentence-case title.
 */
export function SettingsPane() {
	const page = useContext(SettingsPageContext);
	if (!page) return null;
	const layer = page.active;
	return (
		<div className="relative flex min-h-0 flex-1 flex-col" data-testid="settings-page">
			{/* Transparent toolbar-height strip laid over the scroller: keeps the macOS
			  * window-drag region while content scrolls up under it to the panel edge.
			  * The body's top padding clears it (and the fixed titlebar controls that
			  * sit over the panel when the sidebar is closed) at rest. Controls that
			  * scroll beneath it are no-drag (styles.css). The strip itself never takes
			  * pointer events (the native drag region does not need them), so a click
			  * there reaches the control under it, and it stops short of the scrollbar
			  * so the top of the track can still be grabbed. */}
			<div aria-hidden="true" className="pointer-events-none absolute left-0 right-2 top-0 z-10 h-toolbar" style={topbarDragStyle} />
			{/* A covered project layer stays mounted so its draft survives recovery settings above it. */}
			{page.layers.map((entry) => (
				// `relative` makes the scroller the containing block for absolutely
				// positioned content (sr-only labels, menu triggers). Without it they
				// escape to the shell's overflow-x-hidden wrapper, which then scrolls
				// the whole center panel away.
				<div
					aria-busy={!entry.isBodyReady}
					className={cn("settings-thin-scrollbar relative min-h-0 flex-1 overflow-y-auto", entry !== layer && "hidden")}
					key={entry.bodyKey}
				>
					<div className="settings-dialog-body mx-auto flex w-full max-w-6xl flex-col gap-4 px-6 pb-12 pt-[calc(var(--spacing-toolbar)+--spacing(4))]">
						<h1 className="mb-8 flex items-center gap-2 px-(--settings-row-inset) text-heading font-medium leading-7 text-foreground">
							{entry.titleIcon && <Coffee aria-hidden="true" className="size-6" />}
							{entry.title}
							{entry.remoteHostId && <span className="ml-2 text-xs font-normal text-muted-foreground">· {labelForHost(entry.remoteHostId) ?? entry.remoteHostId}</span>}
						</h1>
						{entry.body()}
					</div>
				</div>
			))}
		</div>
	);
}

/** Save progress / failure for the project form, shown under the section list. */
export function SettingsSaveStatus({ layer: layerProp }: { layer?: SettingsLayerState }) {
	const { t } = useTranslation();
	const pageLayer = useSettingsPage();
	const layer = layerProp ?? pageLayer;
	if (!layer?.showSaveStatus) return null;
	const { projectSaveState, remoteHostId } = layer;
	return (
		<div className="mt-2 border-t border-(--color-border-settings-dialog-header) px-2 py-3 text-xs" role="status" aria-live="polite">
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
					<><Loader2 className="size-4 shrink-0 animate-spin" aria-hidden="true" />{t("settings.project.saving")}</>
				</p>
			)}
		</div>
	);
}

type SettingsLayerState = ReturnType<typeof useSettingsLayer>;

/** Project settings: a dialog over the shell, sharing the page's section list and body. */
function ProjectSettingsModal({ layer, covered }: { layer: SettingsLayerState; covered: boolean }) {
	const { t } = useTranslation();
	const contentRef = useRef<HTMLDivElement>(null);
	const restoreFocusOnClose = useRestoreFocusOnClose(!covered);
	// The body renders into a detached host so it stays mounted (and keeps its draft)
	// while recovery settings cover the dialog, then is re-attached when it returns.
	const [bodyHost] = useState(() => document.createElement("div"));
	useEffect(() => { bodyHost.className = "flex min-h-0 flex-1 flex-col"; }, [bodyHost]);
	return (
		<>
		{createPortal(layer.body(), bodyHost)}
		<Dialog.Root open={!covered} onOpenChange={(open) => { if (!open) layer.close(); }}>
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
						"fixed left-1/2 top-1/2 z-overlay h-[min(40rem,calc(100vh-3rem))] w-(--size-settings-dialog-wide) max-h-none -translate-x-1/2 -translate-y-1/2 origin-center overflow-hidden p-0 animate-modal-in motion-reduce:animate-none sm:rounded-lg",
					)}
					onOpenAutoFocus={(event) => {
						// Land on the current section (or the first field), not the close button.
						const root = contentRef.current;
						const target = root?.querySelector<HTMLElement>('nav button[aria-current="page"]:not(:disabled)')
							?? root?.querySelector<HTMLElement>('.settings-dialog-body :is(input, textarea, select, button):not(:disabled)');
						if (!target) return;
						event.preventDefault();
						target.focus({ preventScroll: true });
					}}
					onCloseAutoFocus={restoreFocusOnClose}
					onEscapeKeyDown={(event) => {
						const target = event.target instanceof Element ? event.target : null;
						// In-place edits and login flows take Escape themselves.
						if (target?.closest("[data-settings-inline-edit]")) {
							event.preventDefault();
							return;
						}
						if (contentRef.current?.contains(event.target as Node)) return;
						const activeElement = document.activeElement instanceof Element ? document.activeElement : null;
						const nestedPopup = [target, activeElement].some((element) => element?.closest('[role="menu"], [role="listbox"], [data-radix-popper-content-wrapper]'));
						if (nestedPopup) event.preventDefault();
					}}
					ref={contentRef}
				>
					<div className="flex h-full min-h-0">
						<aside className="flex w-48 shrink-0 flex-col border-r border-(--color-border-settings-dialog-header) bg-card">
							<p className="px-3 pb-1 pt-1.5 text-2xs font-medium tracking-normal text-muted-foreground/60">{t("shell.projectSettings")}</p>
							<nav aria-label={t("settings.navSectionsAria")} className="flex flex-col gap-0.5 p-2 pt-0">
								{layer.navItems.map(({ id, label, icon: Icon, active, disabled, onSelect }) => (
									<button
										aria-current={active ? "page" : undefined}
										className={cn(
											NAV_ROW_HIGHLIGHT_HOST_CLASS,
											"flex h-8 w-full items-center gap-2 rounded-lg px-2.5 text-left text-sm font-medium text-muted-foreground transition-none focus:outline-none focus-visible:outline-none focus-visible:ring-0 disabled:cursor-not-allowed disabled:opacity-50",
										)}
										data-active={active}
										disabled={disabled}
										key={id}
										onClick={onSelect}
										type="button"
									>
										<NavRowHighlight active={active} disabled={disabled} />
										<Icon aria-hidden="true" className="relative z-[1] size-icon-md shrink-0" />
										<span className="relative z-[1] min-w-0 flex-1 truncate">{label}</span>
									</button>
								))}
							</nav>
							<div className="mt-auto"><SettingsSaveStatus layer={layer} /></div>
						</aside>
						<div className="flex min-w-0 flex-1 flex-col bg-card">
							<DialogHeader className={cn(settingsDialogHeaderClass, "flex h-auto shrink-0 flex-row items-center justify-between border-b-0 py-3 pl-(--size-modal-padding) pr-3")}>
								<Dialog.Title className="settings-dialog-title">{layer.title}{layer.remoteHostId && <span className="ml-2 text-xs font-normal text-muted-foreground">· {labelForHost(layer.remoteHostId) ?? layer.remoteHostId}</span>}</Dialog.Title>
								<Dialog.Description className="sr-only">{t("settings.project.dialogDescription")}</Dialog.Description>
								<button aria-label={t("settings.close")} className="settings-close-button" disabled={layer.cueBusy} onClick={layer.close} type="button">
									<X aria-hidden="true" className="size-4" />
								</button>
							</DialogHeader>
							<div aria-busy={!layer.isBodyReady} className={cn(settingsDialogBodyClass, "settings-dialog-body flex-1 px-(--size-modal-padding) pt-0")}>
								<div className="flex min-h-0 flex-1 flex-col" ref={(node) => { if (node && bodyHost.parentElement !== node) node.appendChild(bodyHost); }} />
							</div>
						</div>
					</div>
				</Dialog.Content>
			</Dialog.Portal>
		</Dialog.Root>
		</>
	);
}
