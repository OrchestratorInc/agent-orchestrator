import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { previewDocumentThemeStyle, type ThemePreference, type ThemeStyle } from "../../lib/theme";
import { THEMES, getThemeDefinition } from "../../lib/theme-palettes";
import type { AppLocale } from "../../i18n";
import { useLocaleStore } from "../../stores/locale-store";
import { useSoundNotificationsStore } from "../../stores/sound-notifications-store";
import { useUiStore, type ChatSendKeyMode } from "../../stores/ui-store";
import { isNewSidebarDesign } from "../../lib/new-sidebar-design";
import { useTelemetryPolicyStore } from "../../stores/telemetry-policy-store";
import { useAnalyticsOptOutStore } from "../../stores/analytics-opt-out-store";
import { CliLinkSettings } from "./CliLinkSettingsRow";
import { CliShadowSettingsRow } from "./CliShadowSettingsRow";
import { openChromeAgentsSettings } from "./BrowserChromeSection";
import { Button } from "../ui/button";
import { ArrowRight } from "@/components/app-icons";
import { useTerminalShellStore } from "../../stores/terminal-shell-store";
import { checkTerminalShellPath, useTerminalShells } from "../../hooks/useTerminalShells";
import { DefaultCloudSandboxSection } from "./DefaultCloudSandboxRow";
import { SettingsOptionMenu, type SettingsOption } from "./SettingsOptionMenu";
import { SettingsInlineInput, SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";
import { ThemePaletteSwatch } from "./ThemePaletteSwatch";
import { Switch } from "../ui/switch";
import { cn } from "../../lib/utils";
import { useSettings, useUpdateSessionInterface, useUpdateSessionRetention, useUpdateVersionRefresh } from "../../hooks/useSettings";
import { useEditorPreference, useSetPreferredEditor } from "../../hooks/useEditorHandoff";
import { TargetIcon } from "../TopbarOpenEditorButton";
import type { EditorId } from "../../../shared/editor-handoff";
import type { SessionMode } from "../../types/workspace";
import type { TerminalShellKind } from "../../../shared/ui-locale";
import { isMacPlatform, isWindowsPlatform } from "../../lib/platform";
import type { TerminalClickMode } from "../../lib/terminal-mouse";

/**
 * Default interface for new sessions. Daemon-owned so `ao spawn` and mobile
 * resolve the same value. Only affects sessions created afterwards — a
 * session's interface is fixed when it is born.
 */
function SessionInterfaceRow() {
	const { t } = useTranslation();
	const { settings, isLoading, error } = useSettings();
	const { update, saving, error: saveError } = useUpdateSessionInterface();
	const interfaceOptions = [
		{ value: "tui", label: t("settings.sessionInterface.terminal") },
		{ value: "chat", label: t("settings.sessionInterface.chat") },
	] satisfies SettingsOption<SessionMode>[];

	const chatAvailable = (settings?.chatHarnesses.length ?? 0) > 0;
	// Silent when everything works; speak up only when the control is limited
	// (no chat-capable agent installed) or a save failed.
	const note = saveError ?? error ?? (!chatAvailable ? t("settings.sessionInterface.unavailable") : null);

	const failed = Boolean(saveError || error);

	return (
		<SettingsRow
			label={t("settings.sessionInterface.label")}
			description={note ? <span className={cn(failed && "text-error")} role={failed ? "alert" : undefined}>{note}</span> : undefined}
		>
			<SettingsOptionMenu
				aria-label={t("settings.sessionInterface.label")}
				value={settings?.defaultSessionMode ?? "tui"}
				options={interfaceOptions}
				onChange={(mode) => update(mode)}
				disabled={isLoading || saving || !chatAvailable}
			/>
		</SettingsRow>
	);
}

/**
 * The editor that "Open in editor" uses from the session top bar and session
 * menus. Electron main stores it; opening a session in another editor from the
 * top bar changes it too.
 */
function DefaultEditorRow() {
	const { t } = useTranslation();
	const preference = useEditorPreference();
	const setPreferredEditor = useSetPreferredEditor();
	const editors = preference.data?.editors ?? [];
	const preferredEditorId = preference.data?.preferredEditorId;
	const options: SettingsOption<string>[] = editors.map((editor) => ({
		value: editor.id,
		label: editor.name,
		icon: <TargetIcon target={editor} className="size-icon-sm shrink-0" />,
	}));
	const failed = setPreferredEditor.isError || preference.isError;
	const note = setPreferredEditor.isError
		? t("settings.defaultEditor.saveFailed")
		: preference.isError
			? t("settings.defaultEditor.loadFailed")
			: preference.data && editors.length === 0
				? t("editor.noEditorInstalled")
				: preference.data && !editors.some((editor) => editor.id === preferredEditorId)
					? t("settings.defaultEditor.notInstalled")
					: null;

	return (
		<SettingsRow
			label={t("settings.defaultEditor.label")}
			description={note ? <span className={cn(failed && "text-error")} role={failed ? "alert" : undefined}>{note}</span> : undefined}
		>
			<SettingsOptionMenu
				aria-label={t("settings.defaultEditor.label")}
				value={preferredEditorId ?? ""}
				options={options}
				placeholder={t("editor.chooseEditor")}
				onChange={(editorId) => setPreferredEditor.mutate(editorId as EditorId)}
				disabled={preference.isPending || setPreferredEditor.isPending || editors.length === 0}
			/>
		</SettingsRow>
	);
}

/**
 * After a harness update, idle open sessions restart on the new release
 * instead of running the old one until they happen to restart. Daemon-owned:
 * the daemon does the moving.
 */
function VersionRefreshRow() {
	const { t } = useTranslation();
	const { settings, isLoading } = useSettings();
	const { update, saving, error } = useUpdateVersionRefresh();
	return (
		<div className="flex w-full flex-col">
			<SettingsRow className="rounded-none" label={t("settings.versionRefresh.label")} description={t("settings.versionRefresh.description")}>
				<Switch
					aria-label={t("settings.versionRefresh.label")}
					checked={settings?.versionRefresh ?? false}
					disabled={isLoading || saving || !settings}
					onCheckedChange={update}
				/>
			</SettingsRow>
			{error ? (
				<p role="alert" className="px-3 pb-2 text-caption leading-4 text-error">
					{error}
				</p>
			) : null}
		</div>
	);
}

const SESSION_RETENTION_DAYS = [7, 30, 90];

/**
 * How long an archived session is kept before the daemon deletes it for good.
 * Only sessions whose worktree is already cleaned up are deleted. Hidden on a
 * daemon that predates the setting, where the choice would do nothing.
 */
function SessionRetentionRow() {
	const { t } = useTranslation();
	const { settings, isLoading } = useSettings();
	const { update, saving, error } = useUpdateSessionRetention();
	const days = settings?.sessionRetentionDays;
	if (days === undefined) return null;
	// A value set through the API outside the menu still shows as itself.
	const choices = SESSION_RETENTION_DAYS.includes(days) || days === 0 ? SESSION_RETENTION_DAYS : [...SESSION_RETENTION_DAYS, days].sort((a, b) => a - b);
	const options: SettingsOption<string>[] = [
		{ value: "0", label: t("settings.sessionRetention.never") },
		...choices.map((choice) => ({ value: String(choice), label: t("settings.sessionRetention.afterDays", { days: choice }) })),
	];
	return (
		<SettingsRow
			label={t("settings.sessionRetention.label")}
			description={error ? <span role="alert" className="text-error">{error}</span> : t("settings.sessionRetention.description")}
		>
			<SettingsOptionMenu
				aria-label={t("settings.sessionRetention.label")}
				value={String(days)}
				options={options}
				onChange={(next) => update(Number(next))}
				disabled={isLoading || saving}
			/>
		</SettingsRow>
	);
}

const WINDOWS_SHELL_KINDS = ["pwsh", "powershell", "cmd", "git-bash", "wsl"] as const;
// A shell picked from the detected list on macOS and Linux is stored as a
// custom path, so the stored setting keeps one shape on every platform.
const DETECTED_SHELL_PREFIX = "path:";

type ShellOption = SettingsOption<string> & { path?: string };

function TerminalShellRows() {
	const { t } = useTranslation();
	const preference = useTerminalShellStore((state) => state.preference);
	const load = useTerminalShellStore((state) => state.load);
	const setPreference = useTerminalShellStore((state) => state.setPreference);
	const saving = useTerminalShellStore((state) => state.saving);
	const saveError = useTerminalShellStore((state) => state.saveError);
	const { data: catalog } = useTerminalShells();
	const windows = isWindowsPlatform();
	const [customOpen, setCustomOpen] = useState(false);
	const [customPath, setCustomPath] = useState(preference.path ?? "");
	const [pathError, setPathError] = useState(false);
	const [checking, setChecking] = useState(false);

	useEffect(() => {
		void load();
	}, [load]);

	useEffect(() => {
		setCustomPath(preference.path ?? "");
	}, [preference.path]);

	const windowsLabels: Record<string, string> = {
		pwsh: t("settings.terminalShell.pwsh"),
		powershell: t("settings.terminalShell.windowsPowerShell"),
		cmd: t("settings.terminalShell.cmd"),
		"git-bash": t("settings.terminalShell.gitBash"),
		wsl: t("settings.terminalShell.wsl"),
	};
	const detected = catalog?.shells ?? [];
	const options: ShellOption[] = [{ value: "auto", label: t("settings.terminalShell.auto"), path: catalog?.default.path }];
	if (windows) {
		for (const kind of WINDOWS_SHELL_KINDS) {
			const shell = detected.find((item) => item.id === kind);
			if (shell || !catalog || preference.kind === kind) options.push({ value: kind, label: windowsLabels[kind], path: shell?.path });
		}
	} else {
		for (const shell of detected) {
			options.push({ value: DETECTED_SHELL_PREFIX + shell.path, label: shell.name, path: shell.path });
		}
	}
	options.push({ value: "custom", label: t("settings.terminalShell.custom") });

	let selected: string = preference.kind;
	if (customOpen) selected = "custom";
	else if (preference.kind === "custom" && preference.path && options.some((option) => option.value === DETECTED_SHELL_PREFIX + preference.path)) {
		selected = DETECTED_SHELL_PREFIX + preference.path;
	}
	if (!options.some((option) => option.value === selected)) {
		options.splice(options.length - 1, 0, { value: selected, label: windowsLabels[selected] ?? selected });
	}

	const choose = (value: string) => {
		setPathError(false);
		if (value === "custom") {
			setCustomOpen(true);
			setCustomPath(preference.path ?? "");
			return;
		}
		setCustomOpen(false);
		if (value.startsWith(DETECTED_SHELL_PREFIX)) {
			void setPreference({ kind: "custom", path: value.slice(DETECTED_SHELL_PREFIX.length) });
			return;
		}
		void setPreference({ kind: value as TerminalShellKind });
	};

	const commitCustomPath = async (value: string) => {
		const path = value.trim();
		if (!path || (preference.kind === "custom" && path === preference.path)) {
			setCustomPath(preference.path ?? "");
			setPathError(false);
			return;
		}
		setChecking(true);
		try {
			const result = await checkTerminalShellPath(path);
			setPathError(!result.executable);
			if (result.executable) await setPreference({ kind: "custom", path });
		} catch {
			setPathError(true);
		} finally {
			setChecking(false);
		}
	};

	return (
		<>
			<SettingsRow
				label={t("settings.terminalShell.label")}
				description={
					saveError ? <span role="alert" className="text-error">{t("settings.terminalShell.saveFailed")}</span> : t("settings.terminalShell.hint")
				}
			>
				<SettingsOptionMenu
					aria-label={t("settings.terminalShell.label")}
					value={selected}
					options={options}
					disabled={saving}
					onChange={choose}
					menuClassName="max-w-[28rem]"
					renderMenuItem={(option: ShellOption) => (
						<span className="flex min-w-0 flex-1 items-baseline gap-3">
							<span className="shrink-0">{option.label}</span>
							{option.path ? (
								<span className="ml-auto min-w-0 truncate font-mono text-xs text-settings-muted" title={option.path}>
									{option.path}
								</span>
							) : null}
						</span>
					)}
				/>
			</SettingsRow>
			{selected === "custom" ? (
				<SettingsRow
					label={t("settings.terminalShell.customPath")}
					description={pathError ? <span role="alert" className="text-error">{t("settings.terminalShell.notExecutable")}</span> : undefined}
				>
					<SettingsInlineInput
						id="terminal-shell-custom-path"
						label={t("settings.terminalShell.customPath")}
						value={customPath}
						onChange={(next) => {
							setCustomPath(next);
							setPathError(false);
						}}
						onCommit={(path) => void commitCustomPath(path)}
						onCancel={() => setCustomPath(preference.path ?? "")}
						placeholder={checking ? t("settings.terminalShell.checking") : windows ? t("settings.terminalShell.customPathPlaceholder") : t("settings.terminalShell.customPathPlaceholderUnix")}
					/>
				</SettingsRow>
			) : null}
		</>
	);
}

const COLOR_THEME_OPTIONS = THEMES.map(({ id, label }) => ({ value: id, label })) satisfies SettingsOption<ThemeStyle>[];

export function GeneralSettingsSection({
	titleHidden,
}: {
	titleHidden?: boolean;
}) {
	const { t } = useTranslation();
	const themePreference = useUiStore((state) => state.themePreference);
	const setThemePreference = useUiStore((state) => state.setThemePreference);
	const themeStyle = useUiStore((state) => state.themeStyle);
	const setThemeStyle = useUiStore((state) => state.setThemeStyle);
	const resolvedTheme = useUiStore((state) => state.resolvedTheme);
	const locale = useLocaleStore((state) => state.locale);
	const setLocale = useLocaleStore((state) => state.setLocale);
	const localeSaving = useLocaleStore((state) => state.saving);
	const localeSaveError = useLocaleStore((state) => state.saveError);
	const soundNotificationsEnabled = useSoundNotificationsStore((state) => state.enabled);
	const setSoundNotificationsEnabled = useSoundNotificationsStore((state) => state.setEnabled);
	const soundNotificationsSaving = useSoundNotificationsStore((state) => state.saving);
	const soundNotificationsSaveError = useSoundNotificationsStore((state) => state.saveError);
	const developerMode = useUiStore((state) => state.developerMode);
	const setDeveloperMode = useUiStore((state) => state.setDeveloperMode);
	const navigationGestures = useUiStore((state) => state.navigationGestures);
	const setNavigationGestures = useUiStore((state) => state.setNavigationGestures);
	const renderMath = useUiStore((state) => state.renderMath);
	const setRenderMath = useUiStore((state) => state.setRenderMath);
	const terminalCopyOnSelect = useUiStore((state) => state.terminalCopyOnSelect);
	const setTerminalCopyOnSelect = useUiStore((state) => state.setTerminalCopyOnSelect);
	const terminalClickMode = useUiStore((state) => state.terminalClickMode);
	const setTerminalClickMode = useUiStore((state) => state.setTerminalClickMode);
	const terminalClickOptions = [
		{ value: "app", label: t("settings.terminalClicks.app") },
		{ value: "select", label: t("settings.terminalClicks.select") },
	] satisfies SettingsOption<TerminalClickMode>[];
	const projectAvatars = useUiStore((state) => state.projectAvatars);
	const setProjectAvatars = useUiStore((state) => state.setProjectAvatars);
	const newSidebarDesign = useUiStore(isNewSidebarDesign);
	const chatSendKeyMode = useUiStore((state) => state.chatSendKeyMode);
	const setChatSendKeyMode = useUiStore((state) => state.setChatSendKeyMode);
	const chatSendKeyOptions = [
		{ value: "enter", label: t("settings.chatSendKey.enter") },
		{ value: "mod-enter", label: t("settings.chatSendKey.modEnter") },
	] satisfies SettingsOption<ChatSendKeyMode>[];

	// Trigger and menu rows share one layout: name, then the palette on the right.
	const renderColorTheme = (option: SettingsOption<ThemeStyle> | undefined, compact = false) =>
		option ? (
			<>
				<span className="min-w-0 flex-1 truncate">{option.label}</span>
				<ThemePaletteSwatch palette={getThemeDefinition(option.value)[resolvedTheme]} compact={compact} />
			</>
		) : null;

	const themeOptions = [
		{ value: "light", label: t("settings.theme.light") },
		{ value: "dark", label: t("settings.theme.dark") },
		{ value: "system", label: t("settings.theme.system") },
	] satisfies SettingsOption<ThemePreference>[];

	const languageOptions = [
		{ value: "en", label: t("settings.language.en") },
		{ value: "zh-CN", label: t("settings.language.zhCN") },
		{ value: "ja", label: t("settings.language.ja") },
		{ value: "ko", label: t("settings.language.ko") },
		{ value: "es", label: t("settings.language.es") },
		{ value: "fr", label: t("settings.language.fr") },
		{ value: "de", label: t("settings.language.de") },
		{ value: "pt-BR", label: t("settings.language.ptBR") },
		{ value: "ru", label: t("settings.language.ru") },
	] satisfies SettingsOption<AppLocale>[];

	return (
		<>
			{/* Appearance */}
			<SettingsSection title={t("settings.appearance")} titleHidden={titleHidden} grouped>
				<SettingsRow label={t("settings.theme")}>
					<div className="flex items-center gap-1.5">
						<SettingsOptionMenu
							aria-label={t("settings.colorTheme")}
							value={themeStyle}
							options={COLOR_THEME_OPTIONS}
							onChange={setThemeStyle}
							onHighlight={(previewed) => previewDocumentThemeStyle(previewed, useUiStore.getState().themeStyle)}
							menuClassName="min-w-60"
							renderTrigger={(selected) => renderColorTheme(selected, true)}
							renderMenuItem={(option) => renderColorTheme(option)}
						/>
						<SettingsOptionMenu
							aria-label={t("settings.theme")}
							value={themePreference}
							options={themeOptions}
							onChange={setThemePreference}
						/>
					</div>
				</SettingsRow>
				{isMacPlatform() && <WindowTranslucencyRow />}
				{newSidebarDesign && (
					<SettingsRow label={t("settings.projectAvatars")} description={t("settings.projectAvatars.description")}>
						<Switch aria-label={t("settings.projectAvatars")} checked={projectAvatars} onCheckedChange={setProjectAvatars} />
					</SettingsRow>
				)}
				<SettingsRow
					label={t("settings.language")}
					description={localeSaveError ? <span role="alert" className="text-error">{t("settings.language.saveFailed")}</span> : undefined}
				>
					<SettingsOptionMenu
						aria-label={t("settings.language")}
						disabled={localeSaving}
						value={locale}
						options={languageOptions}
						onChange={(next) => {
							void setLocale(next);
						}}
					/>
				</SettingsRow>
				<SettingsRow label={t("settings.renderMath")} description={t("settings.renderMathHint")}>
					<Switch aria-label={t("settings.renderMath")} checked={renderMath} onCheckedChange={setRenderMath} />
				</SettingsRow>
			</SettingsSection>

			<SettingsSection title={t("settings.navigation")} grouped>
				<SettingsRow label={t("settings.navigationGestures")} description={t("settings.navigationGesturesHint")}>
					<Switch
						aria-label={t("settings.navigationGestures")}
						checked={navigationGestures}
						onCheckedChange={setNavigationGestures}
					/>
				</SettingsRow>
			</SettingsSection>

			{/* Sessions */}
			<SettingsSection title={t("settings.sessions")} grouped>
				<SessionInterfaceRow />
				<DefaultEditorRow />
				<SettingsRow label={t("settings.chatSendKey.label")}>
					<SettingsOptionMenu
						aria-label={t("settings.chatSendKey.label")}
						value={chatSendKeyMode}
						options={chatSendKeyOptions}
						onChange={setChatSendKeyMode}
					/>
				</SettingsRow>
				<VersionRefreshRow />
				<SessionRetentionRow />
				<TerminalShellRows />
				<SettingsRow label={t("settings.terminalCopyOnSelect")}>
					<Switch
						aria-label={t("settings.terminalCopyOnSelect")}
						checked={terminalCopyOnSelect}
						onCheckedChange={setTerminalCopyOnSelect}
					/>
				</SettingsRow>
				<SettingsRow
					label={t("settings.terminalClicks")}
					description={isMacPlatform() ? t("settings.terminalClicks.description.mac") : t("settings.terminalClicks.description.other")}
				>
					<SettingsOptionMenu
						aria-label={t("settings.terminalClicks")}
						value={terminalClickMode}
						options={terminalClickOptions}
						onChange={setTerminalClickMode}
					/>
				</SettingsRow>
				<SettingsRow
					label={t("settings.soundNotifications")}
					description={soundNotificationsSaveError ? <span role="alert" className="text-error">{t("settings.soundNotifications.saveFailed")}</span> : undefined}
				>
					<Switch
						aria-label={t("settings.soundNotifications")}
						checked={soundNotificationsEnabled}
						disabled={soundNotificationsSaving}
						onCheckedChange={(next) => {
							void setSoundNotificationsEnabled(next);
						}}
					/>
				</SettingsRow>
			</SettingsSection>

			<DefaultCloudSandboxSection />

			<SettingsSection title={t("settings.privacy")} grouped>
				<TelemetryEventsRow />
				<AnalyticsRow />
			</SettingsSection>

			{/* Advanced */}
			<SettingsSection title={t("settings.advanced")} grouped>
				<SettingsRow label={t("settings.developerMode")}>
					<Switch
						aria-label={t("settings.developerMode")}
						checked={developerMode}
						onCheckedChange={setDeveloperMode}
					/>
				</SettingsRow>
				{developerMode && <DiagnosticsRow />}
				<CliLinkSettings enabled={developerMode} />
				<CliShadowSettingsRow />
				{developerMode && <SideChatsRow />}
				{developerMode && <SplitPanesRow />}
				{developerMode && <NewSidebarDesignRow />}
				{developerMode && <ChromeAgentsRow />}
			</SettingsSection>
		</>
	);
}

/**
 * PostHog usage analytics opt-out. Independent of the error-event row above: it
 * switches off the renderer, the daemon and a paired phone together.
 */
function AnalyticsRow() {
	const { t } = useTranslation();
	const optedOut = useAnalyticsOptOutStore((state) => state.optedOut);
	const loaded = useAnalyticsOptOutStore((state) => state.loaded);
	const saving = useAnalyticsOptOutStore((state) => state.saving);
	const loadError = useAnalyticsOptOutStore((state) => state.loadError);
	const saveError = useAnalyticsOptOutStore((state) => state.saveError);
	const load = useAnalyticsOptOutStore((state) => state.load);
	const setOptedOut = useAnalyticsOptOutStore((state) => state.setOptedOut);
	useEffect(() => {
		void load();
	}, [load]);
	return (
		<SettingsRow
			label={t("settings.analytics.label")}
			description={
				<span className={cn((loadError || saveError) && "text-error")} role={loadError || saveError ? "alert" : undefined}>
					{t(loadError ? "settings.analytics.loadFailed" : saveError ? "settings.analytics.saveFailed" : "settings.analytics.description")}
				</span>
			}
		>
			<Switch
				aria-label={t("settings.analytics.label")}
				checked={!optedOut}
				disabled={saving || !loaded}
				onCheckedChange={(share) => {
					void setOptedOut(!share);
				}}
			/>
		</SettingsRow>
	);
}

function TelemetryEventsRow() {
	const { t } = useTranslation();
	const view = useTelemetryPolicyStore((state) => state.view);
	const saving = useTelemetryPolicyStore((state) => state.saving);
	const saveError = useTelemetryPolicyStore((state) => state.saveError);
	const setEnabled = useTelemetryPolicyStore((state) => state.setEnabled);
	const checked = view?.eventsEnabled ?? false;
	const blockedEnable = !checked && (view?.environmentVeto || !view?.durabilitySupported);
	const status = saveError ? "failed"
		: !view ? null
		: !view.durabilitySupported ? "unsupported"
		: view.state === "cleanup_failed" ? "failed"
		: view.state === "cleanup_pending" ? "pending"
		: view.reason === "environment_veto" ? "veto"
		: view.reason === "release_blocked" ? "releaseBlocked"
		: null;
	return (
		<SettingsRow
			label={t("settings.telemetryEvents.label")}
			description={
				<span className={cn(status === "failed" && "text-error")} role={status === "failed" ? "alert" : undefined}>
					{t(status ? `settings.telemetryEvents.${status}` : "settings.telemetryEvents.description")}
				</span>
			}
		>
			<Switch aria-label={t("settings.telemetryEvents.label")} checked={checked} disabled={saving || !view || blockedEnable} onCheckedChange={(enabled) => { void setEnabled(enabled); }} />
		</SettingsRow>
	);
}

/**
 * Developer Mode-only toggle for memory and CPU monitoring: the card chips,
 * the board's memory light and window, and the Diagnostics settings page.
 * Off by default so Developer mode alone does not poll process trees.
 */
function DiagnosticsRow() {
	const { t } = useTranslation();
	const diagnostics = useUiStore((state) => state.diagnostics);
	const setDiagnostics = useUiStore((state) => state.setDiagnostics);
	return (
		<SettingsRow label={t("settings.diagnostics")} description={t("settings.diagnosticsToggleHint")}>
			<Switch aria-label={t("settings.diagnostics")} checked={diagnostics} onCheckedChange={setDiagnostics} />
		</SettingsRow>
	);
}

/**
 * Developer Mode-only toggle for side chats. Renderer state like Diagnostics:
 * off hides the inspector tab, /btw, and "Add to side chat".
 */
function SideChatsRow() {
	const { t } = useTranslation();
	const sideChats = useUiStore((state) => state.sideChats);
	const setSideChats = useUiStore((state) => state.setSideChats);
	return (
		<SettingsRow label={t("settings.sideChats")} description={t("settings.sideChatsHint")}>
			<Switch aria-label={t("settings.sideChats")} checked={sideChats} onCheckedChange={setSideChats} />
		</SettingsRow>
	);
}

/** Developer Mode-only switch for dragging center tabs into split panes. Off by default. */
function SplitPanesRow() {
	const { t } = useTranslation();
	const splitPanes = useUiStore((state) => state.splitPanes);
	const setSplitPanes = useUiStore((state) => state.setSplitPanes);
	return (
		<SettingsRow label={t("settings.splitPanes")} description={t("settings.splitPanesHint")}>
			<Switch aria-label={t("settings.splitPanes")} checked={splitPanes} onCheckedChange={setSplitPanes} />
		</SettingsRow>
	);
}

/** Developer Mode-only switch for the new left sidebar design. On by default. */
function NewSidebarDesignRow() {
	const { t } = useTranslation();
	const newSidebarDesign = useUiStore((state) => state.newSidebarDesign);
	const setNewSidebarDesign = useUiStore((state) => state.setNewSidebarDesign);
	return (
		<SettingsRow label={t("settings.newSidebarDesign")} description={t("settings.newSidebarDesignHint")}>
			<Switch aria-label={t("settings.newSidebarDesign")} checked={newSidebarDesign} onCheckedChange={setNewSidebarDesign} />
		</SettingsRow>
	);
}

/**
 * Developer Mode-only entry to Chrome mode. Setup lives with the rest of the
 * browser settings, so this row only takes the user there.
 */
function ChromeAgentsRow() {
	const { t } = useTranslation();
	const openGlobalSettings = useUiStore((state) => state.openGlobalSettings);
	return (
		<SettingsRow label={t("settings.chromeAgents.label")} description={t("settings.chromeAgents.hint")}>
			<Button onClick={() => openChromeAgentsSettings(openGlobalSettings)} size="sm" type="button" variant="ghost">
				{t("settings.chromeAgents.goTo")}
				<ArrowRight aria-hidden="true" className="size-icon-sm" />
			</Button>
		</SettingsRow>
	);
}

/**
 * macOS Appearance toggle for window translucency: native
 * vibrancy blurs the desktop behind a translucent shell. Renderer state.
 */
function WindowTranslucencyRow() {
	const { t } = useTranslation();
	const windowTranslucency = useUiStore((state) => state.windowTranslucency);
	const setWindowTranslucency = useUiStore((state) => state.setWindowTranslucency);
	return (
		<SettingsRow label={t("settings.windowTranslucency")} description={t("settings.windowTranslucencyHint")}>
			<Switch
				aria-label={t("settings.windowTranslucency")}
				checked={windowTranslucency}
				onCheckedChange={setWindowTranslucency}
			/>
		</SettingsRow>
	);
}
