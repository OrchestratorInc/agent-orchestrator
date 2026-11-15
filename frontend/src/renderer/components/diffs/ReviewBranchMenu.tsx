import { markFileMenuPainted, markFileViewerPerformance } from "../../lib/file-viewer-performance";
import { useLayoutEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { GitBranch, GitPullRequest } from "@/components/app-icons";
import { cn } from "../../lib/utils";
import { formatTimeCompact } from "../../lib/format-time";
import { useTruncatedText } from "../../hooks/useTruncatedText";
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
	kind: "local" | "remote" | "pull_request";
	name: string;
	prNumber?: number;
	checkedOut: boolean;
	detached?: boolean;
	updatedAt?: string;
	selected: boolean;
};

export type ReviewBranchTrigger =
	| { kind: "checked_out"; name?: string; detached?: boolean }
	| { kind: "branch"; name: string }
	| { kind: "pull_request"; number: number; branch: string };

export type ReviewBranchMenuProps = {
	trigger: ReviewBranchTrigger;
	state: "loading" | "error" | "ready";
	rows: readonly ReviewBranchRow[];
	truncated: boolean;
	onSelect: (id: string) => void;
	onRetry: () => void;
	onOpenChange?: (open: boolean) => void;
	className?: string;
};

const BRANCH_PAGE = REVIEW_MENU_PAGE;
const SEARCH_THRESHOLD = 1;

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
											<BranchRowLabel label={rowLabel(row)} mono={!row.detached} />
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

function BranchRowLabel({ label, mono }: { label: string; mono: boolean }) {
	const { ref, isTruncated } = useTruncatedText<HTMLSpanElement>(label);
	return (
		<span className={cn("truncate text-xs", mono && "font-mono")} ref={ref} title={isTruncated ? label : undefined}>
			{label}
		</span>
	);
}
