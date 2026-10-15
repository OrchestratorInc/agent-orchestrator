import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { GitBranch, GitPullRequest } from "lucide-react";
import { cn } from "../../lib/utils";
import { formatTimeCompact } from "../../lib/format-time";
import { SettingsMenuTrigger } from "../settings/SettingsMenuTrigger";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuRadioGroup,
	DropdownMenuRadioItem,
	DropdownMenuTrigger,
} from "../ui/dropdown-menu";

export type ReviewBranchRow = {
	id: string;
	/** A `pull_request` row is a PR whose source branch is not local. */
	kind: "local" | "pull_request";
	/** The branch name, a PR's source branch, or "" for a detached HEAD. */
	name: string;
	prNumber?: number;
	checkedOut: boolean;
	detached?: boolean;
	/** ISO timestamp of the tip commit, or of the PR's last update. */
	updatedAt?: string;
	selected: boolean;
};

/** What the trigger names; it is known before the branch list loads. */
export type ReviewBranchTrigger =
	| { kind: "checked_out"; name?: string; detached?: boolean }
	| { kind: "branch"; name: string }
	| { kind: "pull_request"; number: number; branch: string };

export type ReviewBranchMenuProps = {
	trigger: ReviewBranchTrigger;
	state: "loading" | "error" | "ready";
	/** The checked-out row first, then newest first. */
	rows: readonly ReviewBranchRow[];
	/** The daemon capped the list. */
	truncated: boolean;
	/** Called when a row other than the selected one is picked. */
	onSelect: (id: string) => void;
	onRetry: () => void;
	onOpenChange?: (open: boolean) => void;
	className?: string;
};

/** Above this many rows the menu gets a search field. */
const SEARCH_THRESHOLD = 8;

export function ReviewBranchMenu({ trigger, state, rows, truncated, onSelect, onRetry, onOpenChange, className }: ReviewBranchMenuProps) {
	const { t } = useTranslation();
	const [search, setSearch] = useState("");
	const listRef = useRef<HTMLDivElement>(null);

	let triggerLabel: string;
	let monoLabel = true;
	if (trigger.kind === "pull_request") {
		triggerLabel = t("files.branches.prLabel", { number: trigger.number, branch: trigger.branch });
	} else if (trigger.kind === "branch") {
		triggerLabel = trigger.name;
	} else if (trigger.detached) {
		triggerLabel = t("files.branches.detached");
		monoLabel = false;
	} else if (trigger.name) {
		triggerLabel = trigger.name;
	} else {
		// The checked-out branch's name arrives with the list.
		triggerLabel = t("files.branches.checkedOut");
		monoLabel = false;
	}
	const TriggerIcon = trigger.kind === "pull_request" ? GitPullRequest : GitBranch;

	const rowLabel = (row: ReviewBranchRow) => (row.detached ? t("files.branches.detached") : row.name || (row.prNumber ? `#${row.prNumber}` : ""));
	const rowCaption = (row: ReviewBranchRow) => {
		const time = row.updatedAt ? formatTimeCompact(row.updatedAt) : undefined;
		if (row.checkedOut) return time ? t("files.branches.checkedOutUpdated", { time }) : t("files.branches.checkedOut");
		if (!time) return undefined;
		if (row.kind === "pull_request" && row.prNumber) return t("files.branches.pullRequest", { number: row.prNumber, time });
		return t("files.branches.updated", { time });
	};

	const searchable = state === "ready" && rows.length > SEARCH_THRESHOLD;
	const query = searchable ? search.trim().toLocaleLowerCase() : "";
	const visibleRows = query
		? rows.filter((row) => `${rowLabel(row)} ${row.prNumber ? `#${row.prNumber}` : ""}`.toLocaleLowerCase().includes(query))
		: rows;
	const selectedId = rows.find((row) => row.selected)?.id ?? "";

	return (
		<DropdownMenu
			onOpenChange={(open) => {
				if (!open) setSearch("");
				onOpenChange?.(open);
			}}
		>
			<DropdownMenuTrigger asChild>
				<SettingsMenuTrigger
					aria-label={t("files.branches.trigger", { branch: triggerLabel })}
					className={cn("h-control-md min-w-16 max-w-44 shrink-[10] text-xs", className)}
					title={triggerLabel}
				>
					<TriggerIcon aria-hidden="true" className="size-icon-sm shrink-0 text-passive @max-[20rem]/files-header:hidden" />
					<span className={cn("min-w-0 truncate", monoLabel && "font-mono")}>{triggerLabel}</span>
				</SettingsMenuTrigger>
			</DropdownMenuTrigger>
			<DropdownMenuContent align="start" className="w-80 max-w-[calc(100vw-2rem)]">
				{state === "loading" ? (
					<DropdownMenuItem disabled>{t("files.branches.loading")}</DropdownMenuItem>
				) : state === "error" ? (
					<>
						<DropdownMenuItem disabled>{t("files.branches.error")}</DropdownMenuItem>
						<DropdownMenuItem
							onSelect={(event) => {
								event.preventDefault();
								onRetry();
							}}
						>
							{t("files.retry")}
						</DropdownMenuItem>
					</>
				) : (
					<>
						{searchable ? (
							// Typing must not reach the menu's typeahead; ArrowDown moves into
							// the list and Escape still closes the menu.
							<div
								className="shrink-0 pb-1"
								onKeyDown={(event) => {
									if (event.key === "Escape") return;
									event.stopPropagation();
									if (event.key === "ArrowDown") {
										event.preventDefault();
										listRef.current?.querySelector<HTMLElement>("[role='menuitemradio']")?.focus();
									}
								}}
							>
								<input
									aria-label={t("files.branches.search")}
									className="menu-search-input"
									onChange={(event) => setSearch(event.target.value)}
									placeholder={t("files.branches.search")}
									type="search"
									value={search}
								/>
							</div>
						) : null}
						<DropdownMenuRadioGroup
							className="board-scrollbar flex max-h-80 flex-col gap-px overflow-y-auto pr-0.5"
							ref={listRef}
							value={selectedId}
						>
							{visibleRows.map((row) => {
								const Icon = row.kind === "pull_request" ? GitPullRequest : GitBranch;
								const caption = rowCaption(row);
								return (
									<DropdownMenuRadioItem
										key={row.id}
										onSelect={() => {
											if (!row.selected) onSelect(row.id);
										}}
										value={row.id}
									>
										<Icon aria-hidden="true" />
										<span className="flex min-w-0 flex-1 flex-col">
											<span className={cn("truncate text-xs", !row.detached && "font-mono")}>{rowLabel(row)}</span>
											{caption ? <span className="truncate text-caption text-passive">{caption}</span> : null}
										</span>
									</DropdownMenuRadioItem>
								);
							})}
						</DropdownMenuRadioGroup>
						{query && visibleRows.length === 0 ? (
							<DropdownMenuItem disabled>{t("files.branches.noMatches")}</DropdownMenuItem>
						) : null}
						{truncated ? (
							<DropdownMenuItem className="text-caption" disabled>
								{t("files.branches.capped", { count: rows.length })}
							</DropdownMenuItem>
						) : null}
					</>
				)}
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
