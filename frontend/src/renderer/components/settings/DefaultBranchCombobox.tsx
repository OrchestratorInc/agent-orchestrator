import { Check, ChevronDown } from "lucide-react";
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import type { components } from "../../../api/schema";
import { apiClient, apiErrorMessage } from "../../lib/api-client";
import { clientForHost } from "../../lib/host-clients";
import { cn } from "../../lib/utils";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuTrigger } from "../ui/dropdown-menu";
import { SETTINGS_MENU_ITEM, SETTINGS_MENU_SURFACE } from "./SettingsMenuTrigger";

type ProjectBranches = components["schemas"]["ProjectBranches"];
type ProjectBranch = components["schemas"]["ProjectBranch"];

/** Sentinel for "let AO infer each repository's Git default". */
export const DEFAULT_BRANCH_AUTO = "auto";

export const projectBranchesQueryKey = (projectId: string, hostId?: string) =>
	hostId ? ["project-branches", hostId, projectId] as const : ["project-branches", projectId] as const;

/**
 * The default-branch picker that sits beside the hand-editable branch field.
 *
 * Manual entry stays the primary control: the list is a convenience, so a
 * failed read leaves the field usable and the stored value is whatever the user
 * picked or typed — never forced to match a listed branch.
 */
export function DefaultBranchPicker({
	projectId,
	hostId,
	value,
	onChange,
	disabled,
	required,
}: {
	projectId: string;
	hostId?: string;
	value: string;
	onChange: (value: string) => void;
	disabled?: boolean;
	/** Cloud requires a concrete branch, so the auto sentinel is not offered. */
	required?: boolean;
}) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const query = useQuery({
		queryKey: projectBranchesQueryKey(projectId, hostId),
		// A branch list is a hint for a free-text field: a stale list is fine, and
		// a failed read must never block editing the rest of the form.
		enabled: !disabled,
		retry: false,
		staleTime: 30_000,
		queryFn: async ({ signal }): Promise<ProjectBranches> => {
			const client = hostId ? clientForHost(hostId) : apiClient;
			const { data, error } = await client.GET("/api/v1/projects/{id}/branches", {
				params: { path: { id: projectId } },
				signal,
			});
			if (error || !data) throw new Error(apiErrorMessage(error));
			return data;
		},
	});
	const branches: ProjectBranch[] = query.data?.branches ?? [];
	const localBranches = branches.filter((branch) => !branch.remote);
	const remoteBranches = branches.filter((branch) => branch.remote);
	const remoteLabel = remoteBranches[0]?.remote ?? "origin";
	const pick = (next: string) => {
		onChange(next);
		setOpen(false);
	};
	const renderRow = (name: string, label: string) => (
		<DropdownMenuItem
			key={name}
			onSelect={() => pick(name)}
			className={cn(SETTINGS_MENU_ITEM, value === name && "bg-interactive-active text-foreground")}
		>
			<span className="min-w-0 flex-1 truncate">{label}</span>
			{value === name && <Check className="size-3.5 shrink-0" aria-hidden="true" />}
		</DropdownMenuItem>
	);
	return (
		<div className="flex min-w-0 flex-col gap-1">
			<DropdownMenu open={open} onOpenChange={setOpen}>
				<DropdownMenuTrigger asChild>
					<button type="button" className="icon-button shrink-0" aria-label={t("settings.project.defaultBranchBrowse")} disabled={disabled}>
						<ChevronDown className="size-4" aria-hidden="true" />
					</button>
				</DropdownMenuTrigger>
				<DropdownMenuContent align="end" className={cn(SETTINGS_MENU_SURFACE, "max-h-80 overflow-y-auto")}>
					{!required && renderRow(DEFAULT_BRANCH_AUTO, t("settings.project.defaultBranchAuto"))}
					{localBranches.length > 0 && <DropdownMenuLabel>{t("settings.project.defaultBranchLocalGroup")}</DropdownMenuLabel>}
					{localBranches.map((branch) => renderRow(branch.name, branch.name))}
					{remoteBranches.length > 0 && (
						<DropdownMenuLabel>{t("settings.project.defaultBranchRemoteGroup", { remote: remoteLabel })}</DropdownMenuLabel>
					)}
					{remoteBranches.map((branch) => renderRow(branch.name, branch.name))}
					{!query.isPending && branches.length === 0 && query.isError && (
						<p role="status" className="px-2 py-1.5 text-xs text-warning">{t("settings.project.defaultBranchLoadFailed")}</p>
					)}
					{!query.isPending && !query.isError && branches.length === 0 && (
						<p className="px-2 py-1.5 text-xs text-settings-muted">{t("settings.project.defaultBranchEmpty")}</p>
					)}
				</DropdownMenuContent>
			</DropdownMenu>
		</div>
	);
}
