import { Bot, KeyRound, Loader2, MonitorCog, Play, TriangleAlert, type LucideIcon } from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from "react";
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
import { motion } from "motion/react";
import { topbarHeaderClass } from "./TopbarButton";
import { topbarDragStyle, useTopbarPaddingLeft } from "./ShellTopbar";
import { type GlobalSettingsSection, type ProjectSettingsSection, type SettingsModal, useUiStore } from "../stores/ui-store";
import { cn } from "../lib/utils";
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

function useSettingsLayer(settingsModal: SettingsModal | null) {
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

	const activeLabel = !settingsModal ? "" : isProjectSettings
		? (projectSections.find((s) => s.id === activeProjectSection)?.label ?? t("settings.project.general"))
		: globalSettingsItem(activeSection, { cloudEnabled, developerMode, diagnostics, is11x }).label(t);

	const closeSettingsDialog = () => {
		if (!settingsModal) return;
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

	const selectProjectSection = (id: ProjectSettingsSection) => {
		if (projectSaveState.dirty && id !== activeProjectSection) {
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
			projectSaveState.phase === "pending" ||
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
		rootLabel: t("settings.title"),
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
			if (displaySettings?.scope === "project" && !remoteHostId && !isCloudProjectSettings && activeProjectSection === "environment") {
				return <ProjectEnvironmentSettings projectId={displaySettings.projectId} onSaveState={setProjectSaveState} />;
			}
			if (displaySettings?.scope === "project") {
				return <ProjectSettingsForm projectId={displaySettings.projectId} hostId={remoteHostId} cloudOrgId={cloudOrgId} section={activeProjectSection as ProjectFormSection} onSaveState={setProjectSaveState} />;
			}
			return <GlobalSettingsForm cloudEnabled={cloudEnabled} is11x={is11x} focusAgentId={focusAgentId} hostId={displaySettings?.scope === "global" ? displaySettings.hostId : undefined} harnessView={harnessView} section={activeSection} />;
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
	const active = globalModal ? globalLayer : projectModal ? projectLayer : null;
	const activeRef = useRef(active);
	activeRef.current = active;
	const isOpen = active !== null;
	useEffect(() => {
		if (!isOpen) return;
		const onKeyDown = (event: KeyboardEvent) => {
			if (event.key !== "Escape") return;
			const target = event.target instanceof Element ? event.target : null;
			// In-place edits (a profile rename) and login flows (terminal, cloud login panel) take Escape themselves.
			if (target?.closest("[data-settings-inline-edit]")) return;
			// Open menus, listboxes, and dialogs take Escape to dismiss themselves.
			if (document.querySelector('[role="menu"], [role="listbox"], [role="dialog"], [data-radix-popper-content-wrapper]')) return;
			activeRef.current?.close();
		};
		// Capture phase so a field that handles its own Escape cannot swallow the close.
		document.addEventListener("keydown", onKeyDown, true);
		return () => document.removeEventListener("keydown", onKeyDown, true);
	}, [isOpen]);
	return (
		<SettingsPageContext.Provider value={active ? { active, layers: [projectModal ? projectLayer : null, globalModal ? globalLayer : null].filter((layer) => layer !== null) } : null}>
			{children}
		</SettingsPageContext.Provider>
	);
}

/**
 * Settings page rendered inside the center panel, in place of the routed page:
 * the standard app topbar carrying a "Settings / <page>" breadcrumb above a
 * centered, scrolling content column on the normal page background.
 */
export function SettingsPane() {
	const page = useContext(SettingsPageContext);
	const paddingLeft = useTopbarPaddingLeft();
	if (!page) return null;
	const layer = page.active;
	return (
		<div className="flex min-h-0 flex-1 flex-col" data-testid="settings-page">
			<motion.header className={cn(topbarHeaderClass, "workspace-topbar-container")} style={{ ...topbarDragStyle, paddingLeft }}>
				<h1 className="text-brand flex min-w-0 items-center gap-2 font-medium leading-none tracking-tight">
					<span className="text-muted-foreground">{layer.rootLabel}</span>
					<span aria-hidden="true" className="text-muted-foreground/50">/</span>
					<span className="min-w-0 truncate text-foreground">{layer.title}</span>
					{layer.remoteHostId && <span className="truncate text-xs font-normal text-muted-foreground">· {labelForHost(layer.remoteHostId) ?? layer.remoteHostId}</span>}
				</h1>
			</motion.header>
			{/* A covered project layer stays mounted so its draft survives recovery settings above it. */}
			{page.layers.map((entry) => (
				<div
					aria-busy={!entry.isBodyReady}
					className={cn("settings-thin-scrollbar min-h-0 flex-1 overflow-y-auto", entry !== layer && "hidden")}
					key={entry.bodyKey}
				>
					<div className="settings-dialog-body flex w-full flex-col gap-4 px-[18px] pb-12 pt-[18px]">
						{entry.body()}
					</div>
				</div>
			))}
		</div>
	);
}

/** Save progress / failure for the project form, shown under the section list. */
export function SettingsSaveStatus() {
	const { t } = useTranslation();
	const layer = useSettingsPage();
	if (!layer?.showSaveStatus) return null;
	const { projectSaveState, activeProjectSection, remoteHostId } = layer;
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
					{projectSaveState.phase === "pending" && activeProjectSection === "environment" ? t("settings.project.unsavedChanges") : <><Loader2 className="size-4 shrink-0 animate-spin" aria-hidden="true" />{t("settings.project.saving")}</>}
				</p>
			)}
		</div>
	);
}
