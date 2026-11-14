import type { WorkspaceFileSummary } from "../hooks/useSessionWorkspaceFiles";

export type WorkspaceFileStatus = WorkspaceFileSummary["status"];

export const statusLabel: Record<WorkspaceFileStatus, string> = {
	added: "A",
	deleted: "D",
	modified: "M",
	renamed: "R",
	unmodified: "",
};

export const statusTone: Record<WorkspaceFileStatus, string> = {
	added: "text-success",
	deleted: "text-error",
	modified: "text-warning",
	renamed: "text-logo-accent",
	unmodified: "text-passive",
};
