import type { TFunction } from "i18next";
import { isSplitPaneShortcut, type AppShortcutId } from "../../shared/shortcuts";
import { shortcutLabelKeys } from "../i18n/key-maps";
import type { MessageKey } from "../i18n";
import type { GlobalSettingsSection } from "../stores/ui-store";
import type { CommandItem } from "./command-palette";
import { APP_LOCALES, type AppLocale } from "../i18n/locales";
import { useLocaleStore } from "../stores/locale-store";
import { useSoundNotificationsStore } from "../stores/sound-notifications-store";
import { useUiStore, type ChatSendKeyMode } from "../stores/ui-store";
import type { ThemePreference, ThemeStyle } from "./theme";
import { THEMES } from "./theme-palettes";
import type { TerminalClickMode } from "./terminal-mouse";

const SHORTCUT_COMMANDS: { id: AppShortcutId; keywords: string[]; session?: true | "local" }[] = [
	{ id: "keyboard-shortcuts", keywords: ["keybindings", "hotkeys", "keys"] },
	{ id: "previous-session", keywords: ["switch", "cycle"] },
	{ id: "next-session", keywords: ["switch", "cycle"] },
	{ id: "previous-attention-session", keywords: ["needs input", "waiting", "blocked"] },
	{ id: "next-attention-session", keywords: ["needs input", "waiting", "blocked"] },
	{ id: "history-back", keywords: ["navigate", "previous page", "back"] },
	{ id: "history-forward", keywords: ["navigate", "next page", "forward"] },
	{ id: "toggle-sidebar", keywords: ["show", "hide", "left panel"] },
	{ id: "previous-tab", keywords: ["switch tab"], session: true },
	{ id: "next-tab", keywords: ["switch tab"], session: true },
	{ id: "new-shell-terminal", keywords: ["shell", "console", "pty"], session: true },
	{ id: "split-shell-terminal", keywords: ["split", "side by side", "pane", "shell"], session: true },
	{ id: "split-shell-terminal-down", keywords: ["split", "horizontal", "below", "pane", "shell"], session: true },
	{ id: "close-shell-terminal", keywords: ["close terminal"], session: true },
	{ id: "reopen-closed-tab", keywords: ["undo close", "restore tab"], session: true },
	{ id: "archive-session", keywords: ["hide", "done"], session: true },
	{ id: "open-in-editor", keywords: ["vscode", "cursor", "ide", "code", "zed"], session: true },
	{ id: "copy-session-path", keywords: ["worktree", "folder", "directory", "clipboard"], session: "local" },
	{ id: "model-picker", keywords: ["switch model", "llm", "gpt", "claude", "opus", "sonnet"], session: true },
	{ id: "toggle-inspector", keywords: ["show", "hide", "right panel", "details panel"], session: true },
	{ id: "toggle-terminal", keywords: ["show", "hide", "bottom panel", "shell"], session: true },
	{ id: "toggle-diff", keywords: ["show", "hide", "changes", "git diff"], session: true },
];

export type ShortcutCommandContext = {
	splitPanes?: boolean;
	session?: "local" | "elsewhere";
};

export function buildShortcutCommands(t: TFunction, { splitPanes = false, session }: ShortcutCommandContext = {}): CommandItem[] {
	return SHORTCUT_COMMANDS
		.filter(({ id }) => splitPanes || !isSplitPaneShortcut(id))
		.filter((command) => !command.session || (command.session === "local" ? session === "local" : Boolean(session)))
		.map(({ id, keywords }) => ({
			id: `shortcut:${id}`,
			group: "global",
			title: t(shortcutLabelKeys[id]),
			keywords,
			searchOnly: true,
			shortcut: id,
			action: { kind: "run-shortcut", shortcut: id },
		}));
}

type SettingsEntry = {
	id: string;
	section: GlobalSettingsSection;
	label: MessageKey;
	keywords: string[];
	when?: "developerMode" | "mac";
};

const SECTION_KEYWORDS: Partial<Record<GlobalSettingsSection, string[]>> = {
	harness: ["agents", "agent cli", "install", "accounts", "codex", "claude", "subscriptions", "plan", "login"],
	remoteHosts: ["ssh", "remote", "machine", "server"],
	mobile: ["phone", "ios", "android", "pair", "qr code"],
	browserProfiles: ["browser", "profiles", "chrome"],
	computerUse: ["cua", "desktop control", "screen", "automation"],
	updates: ["upgrade", "release", "version"],
	shortcuts: ["keyboard", "keybindings", "hotkeys"],
	help: ["support"],
};

const SETTINGS_ENTRIES: SettingsEntry[] = [
	{ id: "general", section: "general", label: "settings.general", keywords: ["preferences"] },
	{ id: "appearance", section: "general", label: "settings.appearance", keywords: ["look", "visual"] },
	{ id: "theme", section: "general", label: "settings.theme", keywords: ["dark mode", "light mode", "system"] },
	{ id: "color-theme", section: "general", label: "settings.colorTheme", keywords: ["colors", "accent", "palette", "scheme"] },
	{ id: "transparency", section: "general", label: "settings.windowTranslucency", keywords: ["translucency", "vibrancy", "blur"], when: "mac" },
	{ id: "project-avatars", section: "general", label: "settings.projectAvatars", keywords: ["repo icons", "project icons", "github avatars"] },
	{ id: "language", section: "general", label: "settings.language", keywords: ["locale", "translation", "chinese", "japanese", "spanish"] },
	{ id: "render-math", section: "general", label: "settings.renderMath", keywords: ["latex", "katex", "equations", "formulas", "tex"] },
	{ id: "navigation-gestures", section: "general", label: "settings.navigationGestures", keywords: ["swipe", "trackpad", "back gesture"] },
	{ id: "session-interface", section: "general", label: "settings.sessionInterface.label", keywords: ["chat or terminal", "default view"] },
	{ id: "default-editor", section: "general", label: "settings.defaultEditor.label", keywords: ["preferred editor", "ide", "vscode"] },
	{ id: "chat-send-key", section: "general", label: "settings.chatSendKey.label", keywords: ["enter to send", "newline", "submit"] },
	{ id: "version-refresh", section: "general", label: "settings.versionRefresh.label", keywords: ["auto upgrade sessions"] },
	{ id: "session-retention", section: "general", label: "settings.sessionRetention.label", keywords: ["retention", "cleanup", "purge"] },
	{ id: "terminal-shell", section: "general", label: "settings.terminalShell.label", keywords: ["zsh", "bash", "fish", "powershell", "shell path"] },
	{ id: "copy-on-select", section: "general", label: "settings.terminalCopyOnSelect", keywords: ["terminal copy", "selection", "clipboard"] },
	{ id: "terminal-clicks", section: "general", label: "settings.terminalClicks", keywords: ["terminal mouse", "mouse reporting", "select text", "option drag"] },
	{ id: "sound", section: "general", label: "settings.soundNotifications", keywords: ["sounds", "chime", "alert sound"] },
	{ id: "privacy", section: "general", label: "settings.privacy", keywords: ["data", "tracking"] },
	{ id: "analytics", section: "general", label: "settings.analytics.label", keywords: ["telemetry", "tracking", "opt out", "posthog"] },
	{ id: "error-events", section: "general", label: "settings.telemetryEvents.label", keywords: ["crash reports", "sentry", "telemetry"] },
	{ id: "developer-mode", section: "general", label: "settings.developerMode", keywords: ["advanced", "debug", "experimental"] },
	{ id: "cli-link", section: "general", label: "settings.cliLink.label", keywords: ["cli", "terminal command", "install"], when: "developerMode" },
	{ id: "side-chats", section: "general", label: "settings.sideChats", keywords: ["side chat", "secondary chat"], when: "developerMode" },
	{ id: "split-panes", section: "general", label: "settings.splitPanes", keywords: ["split", "side by side", "panes", "drag tabs"], when: "developerMode" },
	{ id: "harness", section: "harness", label: "settings.harness", keywords: [] },
	{ id: "harness-logins", section: "harness", label: "settings.harness.viewLabel", keywords: ["login", "agent login", "api key", "provider key", "environment variable", "anthropic", "openai", "openrouter"] },
	{ id: "remote-hosts", section: "remoteHosts", label: "settings.remoteHosts", keywords: [] },
	{ id: "remote-enable", section: "remoteHosts", label: "remote.enable", keywords: ["remote access"] },
	{ id: "saved-hosts", section: "remoteHosts", label: "remote.savedHosts", keywords: ["servers", "ssh"] },
	{ id: "connection-password", section: "remoteHosts", label: "remote.connectionPassword", keywords: ["password"] },
	{ id: "mobile", section: "mobile", label: "settings.mobile", keywords: [] },
	{ id: "mobile-devices", section: "mobile", label: "mobile.devices.title", keywords: ["phones", "paired devices"] },
	{ id: "keep-awake", section: "mobile", label: "mobile.keepAwake.label", keywords: ["sleep", "caffeinate", "lid closed", "clamshell"] },
	{ id: "secure-pairing", section: "mobile", label: "mobile.securePairing", keywords: ["tls", "encryption"] },
	{ id: "connection-method", section: "mobile", label: "mobile.connectionMethod", keywords: ["tunnel", "lan", "cloudflared"] },
	{ id: "regenerate-password", section: "mobile", label: "mobile.regenerate", keywords: ["reset password"] },
	{ id: "browser", section: "browserProfiles", label: "settings.browserProfiles", keywords: [] },
	{ id: "create-profile", section: "browserProfiles", label: "settings.browserProfiles.create", keywords: ["browser profile"] },
	{ id: "browser-import", section: "browserProfiles", label: "settings.browserImport.rowLabel", keywords: ["import", "firefox", "safari", "cookies"] },
	{ id: "downloads", section: "browserProfiles", label: "settings.downloads", keywords: ["download history"] },
	{ id: "computer-use", section: "computerUse", label: "settings.computerUse", keywords: [] },
	{ id: "updates", section: "updates", label: "settings.updates", keywords: [] },
	{ id: "auto-updates", section: "updates", label: "settings.updates.automatic", keywords: ["auto update"] },
	{ id: "updates-channel", section: "updates", label: "settings.updates.channel", keywords: ["nightly", "beta", "stable", "release channel"] },
	{ id: "feature-build", section: "updates", label: "settings.updates.featureBuild", keywords: ["preview build", "branch build"] },
	{ id: "version", section: "updates", label: "settings.updates.versionSection", keywords: ["about", "build number"] },
	{ id: "shortcuts", section: "shortcuts", label: "settings.keyboardShortcuts", keywords: [] },
	{ id: "help", section: "help", label: "settings.help", keywords: [] },
	{ id: "diagnostics", section: "diagnostics", label: "settings.diagnostics", keywords: ["performance", "cpu", "memory", "monitor"] },
];

export type PalettePreferences = {
	themePreference: ThemePreference;
	themeStyle: ThemeStyle;
	locale: AppLocale;
	terminalClickMode: TerminalClickMode;
	chatSendKeyMode: ChatSendKeyMode;
	toggles: Partial<Record<ToggleId, boolean>>;
};

const TOGGLES = {
	transparency: (on: boolean) => useUiStore.getState().setWindowTranslucency(on),
	"project-avatars": (on: boolean) => useUiStore.getState().setProjectAvatars(on),
	"render-math": (on: boolean) => useUiStore.getState().setRenderMath(on),
	"navigation-gestures": (on: boolean) => useUiStore.getState().setNavigationGestures(on),
	"copy-on-select": (on: boolean) => useUiStore.getState().setTerminalCopyOnSelect(on),
	sound: (on: boolean) => void useSoundNotificationsStore.getState().setEnabled(on),
	"developer-mode": (on: boolean) => useUiStore.getState().setDeveloperMode(on),
	"side-chats": (on: boolean) => useUiStore.getState().setSideChats(on),
	"split-panes": (on: boolean) => useUiStore.getState().setSplitPanes(on),
} satisfies Record<string, (on: boolean) => void>;
export type ToggleId = keyof typeof TOGGLES;

type Choice = { value: string; label: string };

const CHOICES: Record<string, { options: (t: TFunction) => Choice[]; current: (prefs: PalettePreferences) => string; apply: (value: string) => void }> = {
	theme: {
		options: (t) => (["light", "dark", "system"] as const).map((value) => ({ value, label: t(`settings.theme.${value}`) })),
		current: (prefs) => prefs.themePreference,
		apply: (value) => useUiStore.getState().setThemePreference(value as ThemePreference),
	},
	"color-theme": {
		options: () => THEMES.map(({ id, label }) => ({ value: id, label })),
		current: (prefs) => prefs.themeStyle,
		apply: (value) => useUiStore.getState().setThemeStyle(value as ThemeStyle),
	},
	language: {
		options: (t) => APP_LOCALES.map((value) => ({ value, label: t(`settings.language.${value.replace("-", "")}` as MessageKey) })),
		current: (prefs) => prefs.locale,
		apply: (value) => void useLocaleStore.getState().setLocale(value as AppLocale),
	},
	"terminal-clicks": {
		options: (t) => [{ value: "app", label: t("settings.terminalClicks.app") }, { value: "select", label: t("settings.terminalClicks.select") }],
		current: (prefs) => prefs.terminalClickMode,
		apply: (value) => useUiStore.getState().setTerminalClickMode(value as TerminalClickMode),
	},
	"chat-send-key": {
		options: (t) => [{ value: "enter", label: t("settings.chatSendKey.enter") }, { value: "mod-enter", label: t("settings.chatSendKey.modEnter") }],
		current: (prefs) => prefs.chatSendKeyMode,
		apply: (value) => useUiStore.getState().setChatSendKeyMode(value as ChatSendKeyMode),
	},
};

export function buildChoiceCommands(menu: string, t: TFunction, prefs: PalettePreferences): CommandItem[] {
	const choices = CHOICES[menu];
	if (!choices) return [];
	const current = choices.current(prefs);
	return choices.options(t).map(({ value, label }) => ({
		id: `choice:${menu}:${value}`,
		group: "current",
		title: label,
		checked: value === current,
		keepOpen: true,
		action: { kind: "run", run: () => choices.apply(value) },
	}));
}

export function choiceValue(itemId: string): string | undefined {
	return itemId.startsWith("choice:") ? itemId.split(":").slice(2).join(":") : undefined;
}

type SettingsVisibility = { developerMode: boolean; diagnostics: boolean; mac?: boolean; prefs?: PalettePreferences };

function entryVisible(entry: SettingsEntry, { developerMode, diagnostics, mac = false }: SettingsVisibility): boolean {
	if (entry.section === "diagnostics" && !(developerMode && diagnostics)) return false;
	if (entry.when === "developerMode") return developerMode;
	if (entry.when === "mac") return mac;
	return true;
}

export function buildSettingsCommands(t: TFunction, visibility: SettingsVisibility): CommandItem[] {
	const pageLabel = (section: GlobalSettingsSection) => {
		const page = SETTINGS_ENTRIES.find((entry) => entry.section === section && entry.id === sectionLeadId(section));
		return page ? t(page.label) : undefined;
	};
	return SETTINGS_ENTRIES.filter((entry) => entryVisible(entry, visibility)).map((entry) => {
		const isPage = entry.id === sectionLeadId(entry.section);
		const title = t(entry.label);
		const prefs = visibility.prefs;
		const toggle = prefs && entry.id in TOGGLES ? TOGGLES[entry.id as ToggleId] : undefined;
		const on = Boolean(prefs?.toggles[entry.id as ToggleId]);
		const choices = CHOICES[entry.id];
		const choice = prefs && choices;
		return {
			id: `setting:${entry.id}`,
			group: "settings",
			title,
			subtitle: isPage ? undefined : pageLabel(entry.section),
			keywords: isPage
				? [...entry.keywords, ...(SECTION_KEYWORDS[entry.section] ?? [])]
				: choices ? [...entry.keywords, ...choices.options(t).map((option) => option.label)] : entry.keywords,
			searchOnly: true,
			...(toggle ? { toggled: on, keepOpen: true } : {}),
			...(choice && prefs ? { state: choice.options(t).find((option) => option.value === choice.current(prefs))?.label } : {}),
			action: toggle
				? { kind: "run", run: () => toggle(!on) }
				: choice
					? { kind: "open-view", view: { mode: "choices", menu: entry.id, title } }
					: { kind: "open-settings-section", section: entry.section, ...(isPage ? {} : { focus: title }) },
		};
	});
}

function sectionLeadId(section: GlobalSettingsSection): string {
	switch (section) {
		case "remoteHosts":
			return "remote-hosts";
		case "browserProfiles":
			return "browser";
		case "computerUse":
			return "computer-use";
		default:
			return section;
	}
}
