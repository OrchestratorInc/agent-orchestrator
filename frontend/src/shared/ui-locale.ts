/** UI locales supported across the Electron main, preload, and renderer boundaries. */
export const APP_LOCALES = ["en", "zh-CN", "ja", "ko", "es", "fr", "de", "pt-BR", "ru", "tr"] as const;

export type AppLocale = (typeof APP_LOCALES)[number];

export const DEFAULT_LOCALE: AppLocale = "en";

export const TERMINAL_SHELL_KINDS = ["auto", "git-bash", "pwsh", "powershell", "cmd", "wsl", "custom"] as const;

export type TerminalShellKind = (typeof TERMINAL_SHELL_KINDS)[number];

/**
 * `custom` with a path is any executable, including a shell picked from the
 * detected list on macOS and Linux; the other named kinds are Windows shells.
 */
export interface TerminalShellPreference {
	kind: TerminalShellKind;
	path?: string;
}

export interface UiSettings {
	locale: AppLocale;
	/** Whether AO notifies at all: the bell, its badge, OS alerts and sounds. */
	notificationsEnabled: boolean;
	/** Whether attention-worthy notifications (needs input, ready to merge) also play a sound. */
	soundNotificationsEnabled: boolean;
	/** Which notifications are ignored, when notifications are on. */
	notificationFilters: NotificationFilters;
	/** Shell used for newly opened standalone terminal panes. */
	terminalShell: TerminalShellPreference;
}

/**
 * What the person turned off in Manage notifications. Stored as what is muted,
 * so a kind or machine added later notifies until they turn it off.
 */
export interface NotificationFilters {
	/** Notification kind ids (see renderer/lib/notification-preferences.ts). */
	mutedKinds: string[];
	/** Where notifications come from: "local", "cloud", or "remote:<hostId>". */
	mutedSources: string[];
}

export const DEFAULT_TERMINAL_SHELL: TerminalShellPreference = { kind: "auto" };

export const DEFAULT_UI_SETTINGS: UiSettings = {
	locale: DEFAULT_LOCALE,
	notificationsEnabled: true,
	soundNotificationsEnabled: true,
	notificationFilters: { mutedKinds: [], mutedSources: [] },
	terminalShell: DEFAULT_TERMINAL_SHELL,
};

/** Normalize an unknown value to a supported UI locale. */
export function coerceLocale(raw: unknown): AppLocale {
	if (typeof raw === "string" && (APP_LOCALES as readonly string[]).includes(raw)) {
		return raw as AppLocale;
	}
	return DEFAULT_LOCALE;
}

export function coerceTerminalShell(raw: unknown): TerminalShellPreference {
	if (typeof raw !== "object" || raw === null) return { ...DEFAULT_TERMINAL_SHELL };
	const record = raw as Record<string, unknown>;
	if (typeof record.kind !== "string" || !(TERMINAL_SHELL_KINDS as readonly string[]).includes(record.kind)) {
		return { ...DEFAULT_TERMINAL_SHELL };
	}
	const kind = record.kind as TerminalShellKind;
	if (kind !== "custom") return { kind };
	const customPath = typeof record.path === "string" ? record.path.trim() : "";
	return customPath ? { kind, path: customPath } : { kind };
}

function coerceStringList(raw: unknown): string[] {
	if (!Array.isArray(raw)) return [];
	return [...new Set(raw.filter((item): item is string => typeof item === "string" && item.length > 0))];
}

export function coerceNotificationFilters(raw: unknown): NotificationFilters {
	if (typeof raw !== "object" || raw === null) return { mutedKinds: [], mutedSources: [] };
	const record = raw as Record<string, unknown>;
	return { mutedKinds: coerceStringList(record.mutedKinds), mutedSources: coerceStringList(record.mutedSources) };
}

/** Normalize unknown persisted or IPC data to the supported UI-settings schema. */
export function coerceUiSettings(raw: unknown): UiSettings {
	if (typeof raw !== "object" || raw === null) return { ...DEFAULT_UI_SETTINGS };
	const record = raw as Record<string, unknown>;
	const soundNotificationsEnabled =
		typeof record.soundNotificationsEnabled === "boolean"
			? record.soundNotificationsEnabled
			: DEFAULT_UI_SETTINGS.soundNotificationsEnabled;
	return {
		locale: coerceLocale(record.locale),
		notificationsEnabled:
			typeof record.notificationsEnabled === "boolean"
				? record.notificationsEnabled
				: DEFAULT_UI_SETTINGS.notificationsEnabled,
		soundNotificationsEnabled,
		notificationFilters: coerceNotificationFilters(record.notificationFilters),
		terminalShell: coerceTerminalShell(record.terminalShell),
	};
}
