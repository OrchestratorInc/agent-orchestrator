import { OrchestratorIcon, type AppIcon } from "../components/icons";
import {
	ArrowRight,
	Copy,
	File,
	FileSearch,
	Folder,
	FolderPlus,
	FileText,
	GitPullRequest,
	House,
	Keyboard,
	MessageSquare,
	Settings,
	Plus,
	RotateCcw,
	Sun,
} from "lucide-react";
import type { CommandGroupId, CommandItem } from "./command-palette";

/** Exact-id icons for known actions. Add new command ids here as they ship. */
const COMMAND_ICONS: Record<string, AppIcon> = {
	"current-new-task": Plus,
	"current-open-orchestrator": OrchestratorIcon,
	"current-project-settings": Settings,
	"current-copy-branch": Copy,
	"current-search-files": FileSearch,
	"global-home": House,
	"global-new-project": FolderPlus,
	"global-settings": Settings,
	"global-theme": Sun,
};

/** Fallback icons by group when no exact id match exists. */
const GROUP_ICONS: Partial<Record<CommandGroupId, AppIcon>> = {
	projects: Folder,
	files: File,
	content: FileText,
	messages: MessageSquare,
	settings: Settings,
	prs: GitPullRequest,
	// Sessions / attention stay text-only, matching Cursor's "Chats" rows.
};

/** Icons for a session's actions, keyed by the action they run. */
const SESSION_ACTION_ICONS: Partial<Record<NonNullable<CommandItem["action"]>["kind"], AppIcon>> = {
	navigate: ArrowRight,
	"resume-session": RotateCcw,
	"copy-branch": Copy,
};

export function iconForCommand(item: CommandItem): AppIcon | null {
	if (item.action?.kind === "run-shortcut") return Keyboard;
	if (item.id.startsWith("session-action:") && item.action) return SESSION_ACTION_ICONS[item.action.kind] ?? null;
	return COMMAND_ICONS[item.id] ?? GROUP_ICONS[item.group] ?? null;
}
