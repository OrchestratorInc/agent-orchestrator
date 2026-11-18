import { OrchestratorIcon, type AppIcon } from "../components/icons";
import {
	Archive,
	ArrowDown,
	ArrowLeft,
	ArrowRight,
	ArrowUp,
	Bot,
	Bug,
	CalendarClock,
	Code2,
	Columns2,
	Copy,
	Download,
	File,
	FileSearch,
	Folder,
	FolderPlus,
	FileText,
	Gauge,
	GitCompareArrows,
	GitPullRequest,
	House,
	Keyboard,
	LayoutGrid,
	MessageSquare,
	Package,
	PanelLeft,
	PanelRight,
	Pin,
	Settings,
	Plus,
	RotateCcw,
	Rows2,
	SquareTerminal,
	Sun,
	Terminal,
	Trash2,
	Undo2,
	X,
} from "@/components/app-icons";
import type { AppShortcutId } from "../../shared/shortcuts";
import type { CommandGroupId, CommandItem } from "./command-palette";

/** Exact-id icons for known actions. Add new command ids here as they ship. */
const COMMAND_ICONS: Record<string, AppIcon> = {
	"current-new-task": Plus,
	"current-import-sessions": Download,
	"current-open-orchestrator": OrchestratorIcon,
	"current-project-settings": Settings,
	"current-open-archive": Archive,
	"current-copy-branch": Copy,
	"current-search-files": FileSearch,
	"global-home": House,
	"global-go-to-project": Folder,
	"global-go-to-session": MessageSquare,
	"global-automations": CalendarClock,
	"global-artifacts": Package,
	"global-archived-sessions": Archive,
	"global-new-project": FolderPlus,
	"global-import-sessions": Download,
	"global-usage": Gauge,
	"global-settings": Settings,
	"global-theme": Sun,
	"global-report-problem": Bug,
	"project-action:new-task": Plus,
	"project-action:orchestrator": OrchestratorIcon,
	"project-action:board": LayoutGrid,
	"project-action:archive": Archive,
	"project-action:copy-path": Copy,
	"project-action:pin": Pin,
	"project-action:settings": Settings,
	"project-action:remove": Trash2,
};

/** What each shortcut command does, drawn like the control it stands for. */
const SHORTCUT_ICONS: Partial<Record<AppShortcutId, AppIcon>> = {
	"new-session": Plus,
	"keyboard-shortcuts": Keyboard,
	"previous-session": ArrowUp,
	"next-session": ArrowDown,
	"previous-attention-session": ArrowUp,
	"next-attention-session": ArrowDown,
	"history-back": ArrowLeft,
	"history-forward": ArrowRight,
	"previous-tab": ArrowLeft,
	"next-tab": ArrowRight,
	"new-shell-terminal": SquareTerminal,
	"split-shell-terminal": Columns2,
	"split-shell-terminal-down": Rows2,
	"close-shell-terminal": X,
	"reopen-closed-tab": Undo2,
	"archive-session": Archive,
	"open-in-editor": Code2,
	"copy-session-path": Copy,
	"model-picker": Bot,
	"toggle-sidebar": PanelLeft,
	"toggle-inspector": PanelRight,
	"toggle-terminal": SquareTerminal,
	"toggle-diff": GitCompareArrows,
};

/** Fallback icons by group when no exact id match exists. */
const GROUP_ICONS: Partial<Record<CommandGroupId, AppIcon>> = {
	projects: Folder,
	files: File,
	content: FileText,
	messages: MessageSquare,
	settings: Settings,
	prs: GitPullRequest,
	// Session-text hits get a terminal glyph so they read as transcript text
	// rather than as a command or a file.
	search: Terminal,
	// Sessions / attention stay text-only, matching Cursor's "Chats" rows.
};

/** Icons for a session's actions, keyed by the action they run. */
const SESSION_ACTION_ICONS: Partial<Record<NonNullable<CommandItem["action"]>["kind"], AppIcon>> = {
	navigate: ArrowRight,
	"resume-session": RotateCcw,
	"copy-branch": Copy,
};

export function iconForCommand(item: CommandItem): AppIcon | null {
	if (COMMAND_ICONS[item.id]) return COMMAND_ICONS[item.id];
	if (item.action?.kind === "run-shortcut") return SHORTCUT_ICONS[item.action.shortcut] ?? Keyboard;
	if (item.id.startsWith("session-action:") && item.action) return SESSION_ACTION_ICONS[item.action.kind] ?? null;
	return COMMAND_ICONS[item.id] ?? GROUP_ICONS[item.group] ?? null;
}
