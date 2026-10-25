import { OrchestratorIcon, type AppIcon } from "../components/icons";
import {
	Copy,
	File,
	Folder,
	FolderPlus,
	GitPullRequest,
	House,
	Settings,
	Plus,
	Sun,
} from "lucide-react";
import type { CommandGroupId, CommandItem } from "./command-palette";

/** Exact-id icons for known actions. Add new command ids here as they ship. */
const COMMAND_ICONS: Record<string, AppIcon> = {
	"current-new-task": Plus,
	"current-open-orchestrator": OrchestratorIcon,
	"current-project-settings": Settings,
	"current-copy-branch": Copy,
	"current-search-files": Folder,
	"global-home": House,
	"global-new-project": FolderPlus,
	"global-settings": Settings,
	"global-theme": Sun,
};

/** Fallback icons by group when no exact id match exists. */
const GROUP_ICONS: Partial<Record<CommandGroupId, AppIcon>> = {
	projects: Folder,
	files: File,
	prs: GitPullRequest,
	// Sessions / attention stay text-only, matching Cursor's "Chats" rows.
};

export function iconForCommand(item: CommandItem): AppIcon | null {
	return COMMAND_ICONS[item.id] ?? GROUP_ICONS[item.group] ?? null;
}
