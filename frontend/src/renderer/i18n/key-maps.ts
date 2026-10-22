import type { AppShortcutId, ShortcutCategory } from "../../shared/shortcuts";
import type { MessageKey } from "./messages";

/** Exhaustive mappings keep dynamic domain identifiers inside the typed catalog. */
export const shortcutLabelKeys: Record<AppShortcutId, MessageKey> = {
	"new-session": "shortcut.new-session",
	"new-shell-terminal": "shortcut.new-shell-terminal",
	"close-shell-terminal": "shortcut.close-shell-terminal",
	"reopen-closed-tab": "shortcut.reopen-closed-tab",
	"keyboard-shortcuts": "shortcut.keyboard-shortcuts",
	"command-palette": "shortcut.command-palette",
	"quick-open-file": "shortcut.quick-open-file",
	"open-settings": "shortcut.open-settings",
	"toggle-sidebar": "shortcut.toggle-sidebar",
	"toggle-inspector": "shortcut.toggle-inspector",
	"toggle-terminal": "shortcut.toggle-terminal",
	"toggle-diff": "shortcut.toggle-diff",
	"jump-to-session": "shortcut.jump-to-session",
	"previous-session": "shortcut.previous-session",
	"next-session": "shortcut.next-session",
	"previous-attention-session": "shortcut.previous-attention-session",
	"next-attention-session": "shortcut.next-attention-session",
	"history-back": "shortcut.history-back",
	"history-forward": "shortcut.history-forward",
	"previous-tab": "shortcut.previous-tab",
	"next-tab": "shortcut.next-tab",
	"archive-session": "shortcut.archive-session",
	"open-in-editor": "shortcut.open-in-editor",
	"copy-session-path": "shortcut.copy-session-path",
	"model-picker": "shortcut.model-picker",
	"open-orchestrator": "shortcut.open-orchestrator",
	"toggle-browser-devtools": "titlebar.devtools",
};

export const shortcutCategoryLabelKeys: Record<ShortcutCategory, MessageKey> = {
	General: "shortcut.category.general",
	Navigation: "shortcut.category.navigation",
	Session: "shortcut.category.session",
	Panels: "shortcut.category.panels",
};
