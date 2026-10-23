import { markFileMenuPainted, markFileViewerPerformance } from "../../lib/file-viewer-performance";
import { useLayoutEffect, useRef, useState } from "react";
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
import { REVIEW_MENU_LIST, REVIEW_MENU_MORE, REVIEW_MENU_PAGE, REVIEW_MENU_ROW, REVIEW_MENU_TRIGGER, REVIEW_MENU_VISIBLE_ROWS } from "./review-menu-styles";

export type ReviewBranchRow = {
	id: string;
	/** A `pull_request` row is a PR whose source branch is not local. */
	kind: "local" | "remote" | "pull_request";
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

const BRANCH_PAGE = REVIEW_MENU_PAGE;
/** A list that fits its box needs no search; one that scrolls gets it. */
const SEARCH_THRESHOLD = REVIEW_MENU_VISIBLE_ROWS;

export function ReviewBranchMenu({ trigger, state, rows, truncated, onSelect, onRetry, onOpenChange, className }: ReviewBranchMenuProps) {
	const { t } = useTranslation();
	const [menuOpen,setMenuOpen]=useState(false);
	const [search, setSearch] = useState("");
	const [visibleCount, setVisibleCount] = useState(BRANCH_PAGE);
 useLayoutEffect(()=>{if(menuOpen) markFileMenuPainted();},[menuOpen]);
	const listRef = useRef<HTMLDivElement>(null);

	let triggerLabel: string;
	let monoLabel = true;
	if (trigger.kind === "pull_request") {
		triggerLabel = t("files.branches.prLabel", { number: trigger.number, branch: trigger.branch });
	} else if (trigger.kind === "branch") {
		triggerLabel = trigger.name.replace(/^refs\/remotes\//, "");
	} else if (trigger.detached) {
		triggerLabel = t("files.branches.detached");
		monoLabel = false;
	} else if (trigger.name) {
		triggerLabel = trigger.name.replace(/^refs\/remotes\//, "");
	} else {
		// The checked-out branch's name arrives with the list.
		triggerLabel = t("files.branches.checkedOut");
		monoLabel = false;
	}

	const rowLabel = (row: ReviewBranchRow) => (row.detached ? t("files.branches.detached") : row.name || (row.prNumber ? `#${row.prNumber}` : ""));
	const rowCaption = (row: ReviewBranchRow) => {
		const time = row.updatedAt ? formatTimeCompact(row.updatedAt, { longUnits: true }) : undefined;
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
	// A picked branch further down than the first page stays in reach without paging to it.
	const selectedIndex = visibleRows.findIndex((row) => row.selected);
	const shownCount = visibleRows.length <= REVIEW_MENU_VISIBLE_ROWS
		? visibleRows.length
		: Math.max(visibleCount, Math.ceil((selectedIndex + 1) / BRANCH_PAGE) * BRANCH_PAGE);

	return (
		<DropdownMenu
			onOpenChange={(open) => {
    setMenuOpen(open);
				if (!open) {setSearch("");setVisibleCount(BRANCH_PAGE);} else markFileViewerPerformance("file-menu-open-requested");
				onOpenChange?.(open);
			}}
		>
			<DropdownMenuTrigger asChild>
				<SettingsMenuTrigger
					aria-label={t("files.branches.trigger", { branch: triggerLabel })}
					className={cn(REVIEW_MENU_TRIGGER, "min-w-16 max-w-44 shrink-[10] text-muted-foreground hover:text-foreground data-[state=open]:text-foreground", className)}
					title={triggerLabel}
				>
					<span className={cn("min-w-0 truncate", monoLabel && "font-mono")}>{triggerLabel}</span>
				</SettingsMenuTrigger>
			</DropdownMenuTrigger>
			<DropdownMenuContent align="start" className="w-72 max-w-[calc(100vw-2rem)]">
				{state === "loading" ? (
					<DropdownMenuItem className={REVIEW_MENU_ROW} disabled>{t("files.branches.loading")}</DropdownMenuItem>
				) : state === "error" ? (
					<>
						<DropdownMenuItem className={REVIEW_MENU_ROW} disabled>{t("files.branches.error")}</DropdownMenuItem>
						<DropdownMenuItem
							className={REVIEW_MENU_ROW}
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
									className="menu-search-input h-control-md! px-2! text-xs!"
									onChange={(event) => {setSearch(event.target.value);setVisibleCount(BRANCH_PAGE);}}
									placeholder={t("files.branches.search")}
									type="search"
									value={search}
								/>
							</div>
						) : null}
						{/* The box shows seven 36px rows and their 1px gaps; Show more ends the scroll, as in the commits list. */}
						<DropdownMenuRadioGroup
							className={cn(REVIEW_MENU_LIST, "max-h-[16.125rem]")}
							ref={listRef}
							value={selectedId}
						>
							{visibleRows.slice(0, shownCount).map((row) => {
								const Icon = row.kind === "pull_request" ? GitPullRequest : GitBranch;
								const caption = rowCaption(row);
								return (
									<DropdownMenuRadioItem
										className={cn(REVIEW_MENU_ROW, "h-9 shrink-0")}
										key={row.id}
										onSelect={() => {
											if (!row.selected) onSelect(row.id);
										}}
										value={row.id}
									>
										<Icon aria-hidden="true" />
										<span className="flex min-w-0 flex-1 flex-col leading-tight">
											<span className={cn("truncate text-xs", !row.detached && "font-mono")}>{rowLabel(row)}</span>
											{caption ? <span className="truncate text-caption text-passive">{caption}</span> : null}
										</span>
									</DropdownMenuRadioItem>
								);
							})}
							{visibleRows.length > shownCount ? (
								<DropdownMenuItem
									className={cn(REVIEW_MENU_MORE, "shrink-0")}
									onSelect={(event) => {
										event.preventDefault();
										setVisibleCount(shownCount + BRANCH_PAGE);
									}}
								>
									{t("files.changes.showMore", { count: Math.min(BRANCH_PAGE, visibleRows.length - shownCount) })}
								</DropdownMenuItem>
							) : null}
						</DropdownMenuRadioGroup>
						{query && visibleRows.length === 0 ? (
							<DropdownMenuItem className={REVIEW_MENU_ROW} disabled>{t("files.branches.noMatches")}</DropdownMenuItem>
						) : null}
						{truncated ? (
							<DropdownMenuItem className={cn(REVIEW_MENU_ROW, "text-caption")} disabled>
								{t("files.branches.capped", { count: rows.length })}
							</DropdownMenuItem>
						) : null}
					</>
				)}
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
