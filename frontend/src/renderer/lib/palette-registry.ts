import type { TFunction } from "i18next";
import type { AppShortcutId } from "../../shared/shortcuts";
import { shortcutLabelKeys } from "../i18n/key-maps";
import type { MessageKey } from "../i18n";
import type { GlobalSettingsSection } from "../stores/ui-store";
import type { CommandItem } from "./command-palette";

// What the palette can find by name beyond projects, sessions and PRs:
// keyboard-shortcut actions and individual settings. Labels are the app's own
// localized strings; keywords are English synonyms a user might type instead
// ("dark mode" for Theme). Rows are search-only so the empty palette stays
// short.

/**
 * Shortcut actions offered as palette commands. Left out: the palette's own
 * openers, the indexed jump, and actions the palette already lists under its
 * own commands (New task, Global settings, Open orchestrator).
 */
const SHORTCUT_COMMANDS: { id: AppShortcutId; keywords: string[] }[] = [
	{ id: "keyboard-shortcuts", keywords: ["keybindings", "hotkeys", "keys"] },
	{ id: "previous-session", keywords: ["switch", "cycle"] },
	{ id: "next-session", keywords: ["switch", "cycle"] },
	{ id: "previous-attention-session", keywords: ["needs input", "waiting", "blocked"] },
	{ id: "next-attention-session", keywords: ["needs input", "waiting", "blocked"] },
	{ id: "history-back", keywords: ["navigate", "previous page", "back"] },
	{ id: "history-forward", keywords: ["navigate", "next page", "forward"] },
	{ id: "previous-tab", keywords: ["switch tab"] },
	{ id: "next-tab", keywords: ["switch tab"] },
	{ id: "new-shell-terminal", keywords: ["shell", "console", "pty"] },
	{ id: "close-shell-terminal", keywords: ["close terminal"] },
	{ id: "reopen-closed-tab", keywords: ["undo close", "restore tab"] },
	{ id: "archive-session", keywords: ["hide", "done", "remove"] },
	{ id: "open-in-editor", keywords: ["vscode", "cursor", "ide", "code", "zed"] },
	{ id: "copy-session-path", keywords: ["worktree", "folder", "directory", "clipboard"] },
	{ id: "model-picker", keywords: ["switch model", "llm", "gpt", "claude", "opus", "sonnet"] },
	{ id: "toggle-sidebar", keywords: ["show", "hide", "open", "close", "left panel"] },
	{ id: "toggle-inspector", keywords: ["show", "hide", "open", "close", "right panel", "details panel"] },
	{ id: "toggle-terminal", keywords: ["show", "hide", "open", "close", "bottom panel", "shell"] },
	{ id: "toggle-diff", keywords: ["show", "hide", "open", "close", "changes", "review changes", "git diff"] },
	{ id: "toggle-browser-devtools", keywords: ["show", "hide", "open", "close", "inspect", "web inspector", "console"] },
];

export function buildShortcutCommands(t: TFunction): CommandItem[] {
	return SHORTCUT_COMMANDS.map(({ id, keywords }) => ({
		id: `shortcut:${id}`,
		group: "global",
		title: t(shortcutLabelKeys[id]),
		keywords,
		searchOnly: true,
		action: { kind: "run-shortcut", shortcut: id },
	}));
}

type SettingsEntry = {
	id: string;
	section: GlobalSettingsSection;
	label: MessageKey;
	keywords: string[];
};

/** Keywords every row on a page inherits: "codex" finds Add account on the accounts page. */
const SECTION_KEYWORDS: Partial<Record<GlobalSettingsSection, string[]>> = {
	agents: ["accounts", "codex", "claude", "subscriptions", "plan", "login"],
	harness: ["agents", "agent cli", "install"],
	remoteHosts: ["ssh", "remote", "machine", "server"],
	mobile: ["phone", "ios", "android", "pair", "qr code"],
	browserProfiles: ["browser", "profiles", "chrome"],
	updates: ["upgrade", "release", "version"],
	shortcuts: ["keyboard", "keybindings", "hotkeys"],
	help: ["support", "docs"],
};

const SETTINGS_ENTRIES: SettingsEntry[] = [
	{ id: "general", section: "general", label: "settings.general", keywords: ["preferences"] },
	{ id: "appearance", section: "general", label: "settings.appearance", keywords: ["look", "visual"] },
	{ id: "theme", section: "general", label: "settings.theme", keywords: ["dark mode", "light mode", "appearance"] },
	{ id: "color-theme", section: "general", label: "settings.colorTheme", keywords: ["palette", "colors", "accent"] },
	{ id: "language", section: "general", label: "settings.language", keywords: ["locale", "translation", "chinese", "japanese", "spanish"] },
	{ id: "transparency", section: "general", label: "settings.windowTranslucency", keywords: ["translucency", "vibrancy", "blur"] },
	{ id: "session-interface", section: "general", label: "settings.sessionInterface.label", keywords: ["chat or terminal", "default view"] },
	{ id: "version-refresh", section: "general", label: "settings.versionRefresh.label", keywords: ["auto upgrade sessions", "version refresh"] },
	{ id: "terminal-shell", section: "general", label: "settings.terminalShell.label", keywords: ["zsh", "bash", "fish", "powershell", "shell path"] },
	{ id: "copy-on-select", section: "general", label: "settings.terminalCopyOnSelect", keywords: ["terminal copy", "selection", "clipboard"] },
	{ id: "sound", section: "general", label: "settings.soundNotifications", keywords: ["sounds", "chime", "alert sound"] },
	{ id: "privacy", section: "general", label: "settings.privacy", keywords: ["data", "tracking"] },
	{ id: "analytics", section: "general", label: "settings.analytics.label", keywords: ["telemetry", "tracking", "opt out", "posthog"] },
	{ id: "error-events", section: "general", label: "settings.telemetryEvents.label", keywords: ["crash reports", "sentry", "telemetry"] },
	{ id: "developer-mode", section: "general", label: "settings.developerMode", keywords: ["advanced", "debug"] },
	{ id: "side-chats", section: "general", label: "settings.sideChats", keywords: ["side chat", "secondary chat"] },
	{ id: "cloud", section: "general", label: "settings.cloud", keywords: ["ao cloud", "enable cloud"] },
	{ id: "cli-link", section: "general", label: "settings.cliLink.label", keywords: ["cli", "terminal command", "path", "install"] },
	{ id: "accounts", section: "agents", label: "settings.agents", keywords: [] },
	{ id: "add-account", section: "agents", label: "settings.codexAccounts.add", keywords: ["sign in"] },
	{ id: "remove-account", section: "agents", label: "settings.agentAccounts.remove", keywords: ["logout", "sign out"] },
	{ id: "refresh-limits", section: "agents", label: "settings.agentAccounts.refresh", keywords: ["usage", "rate limit", "quota"] },
	{ id: "usage-limits", section: "agents", label: "settings.codexAccounts.generalUsageLimits", keywords: ["usage", "rate limit", "quota"] },
	{ id: "harness", section: "harness", label: "settings.harness", keywords: [] },
	{ id: "harness-logins", section: "harness", label: "settings.harness.viewLabel", keywords: ["login", "agent login"] },
	{ id: "remote-hosts", section: "remoteHosts", label: "settings.remoteHosts", keywords: [] },
	{ id: "remote-enable", section: "remoteHosts", label: "remote.enable", keywords: [] },
	{ id: "saved-hosts", section: "remoteHosts", label: "remote.savedHosts", keywords: ["servers"] },
	{ id: "connection-password", section: "remoteHosts", label: "remote.connectionPassword", keywords: ["password"] },
	{ id: "mobile", section: "mobile", label: "settings.mobile", keywords: [] },
	{ id: "mobile-devices", section: "mobile", label: "mobile.devices.title", keywords: ["phones", "paired devices"] },
	{ id: "keep-awake", section: "mobile", label: "mobile.keepAwake.label", keywords: ["sleep", "caffeinate"] },
	{ id: "keep-awake-lid", section: "mobile", label: "mobile.keepAwake.lidLabel", keywords: ["sleep", "clamshell"] },
	{ id: "secure-pairing", section: "mobile", label: "mobile.securePairing", keywords: ["tls", "encryption"] },
	{ id: "connection-method", section: "mobile", label: "mobile.connectionMethod", keywords: ["tunnel", "lan", "cloudflared"] },
	{ id: "regenerate-password", section: "mobile", label: "mobile.regenerate", keywords: ["reset password"] },
	{ id: "browser", section: "browserProfiles", label: "settings.browserProfiles", keywords: [] },
	{ id: "create-profile", section: "browserProfiles", label: "settings.browserProfiles.create", keywords: ["browser profile"] },
	{ id: "browser-import", section: "browserProfiles", label: "settings.browserImport.rowLabel", keywords: ["import", "firefox", "safari", "cookies"] },
	{ id: "downloads", section: "browserProfiles", label: "settings.downloads", keywords: ["download history"] },
	{ id: "updates", section: "updates", label: "settings.updates", keywords: [] },
	{ id: "auto-updates", section: "updates", label: "settings.updates.automatic", keywords: ["auto update"] },
	{ id: "updates-channel", section: "updates", label: "settings.updates.channel", keywords: ["nightly", "beta", "stable", "release channel"] },
	{ id: "feature-build", section: "updates", label: "settings.updates.featureBuild", keywords: ["preview build", "branch build"] },
	{ id: "version", section: "updates", label: "settings.updates.versionSection", keywords: ["about", "build number"] },
	{ id: "shortcuts", section: "shortcuts", label: "settings.keyboardShortcuts", keywords: [] },
	{ id: "help", section: "help", label: "settings.help", keywords: [] },
	{ id: "report-problem", section: "help", label: "settings.reportProblem", keywords: ["bug report", "feedback", "issue"] },
	{ id: "diagnostics", section: "diagnostics", label: "settings.diagnostics", keywords: ["performance", "cpu", "memory", "monitor"] },
];

type SettingsVisibility = { developerMode: boolean; diagnostics: boolean };

/** Mirrors the visibility rules of the settings catalog for the pages the registry covers. */
function sectionVisible(section: GlobalSettingsSection, { developerMode, diagnostics }: SettingsVisibility): boolean {
	switch (section) {
		case "agents":
			return !developerMode;
		case "diagnostics":
			return developerMode && diagnostics;
		default:
			return true;
	}
}

export function buildSettingsCommands(t: TFunction, visibility: SettingsVisibility): CommandItem[] {
	const pageLabel = (section: GlobalSettingsSection) => {
		const page = SETTINGS_ENTRIES.find((entry) => entry.section === section && entry.id === sectionLeadId(section));
		return page ? t(page.label) : undefined;
	};
	return SETTINGS_ENTRIES.filter((entry) => sectionVisible(entry.section, visibility)).map((entry) => {
		const isPage = entry.id === sectionLeadId(entry.section);
		return {
			id: `setting:${entry.id}`,
			group: "settings",
			title: t(entry.label),
			subtitle: isPage ? undefined : pageLabel(entry.section),
			keywords: [...entry.keywords, ...(SECTION_KEYWORDS[entry.section] ?? [])],
			searchOnly: true,
			// A page entry opens the page; any other lands on its own row.
			action: { kind: "open-settings-section", section: entry.section, ...(isPage ? {} : { focus: t(entry.label) }) },
		};
	});
}

/** The registry entry naming a whole settings page. */
function sectionLeadId(section: GlobalSettingsSection): string {
	switch (section) {
		case "agents":
			return "accounts";
		case "remoteHosts":
			return "remote-hosts";
		case "browserProfiles":
			return "browser";
		default:
			return section;
	}
}
