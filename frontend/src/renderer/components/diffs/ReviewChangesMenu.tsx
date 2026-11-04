import { markFileMenuPainted, markFileViewerPerformance } from "../../lib/file-viewer-performance";
import { useLayoutEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Check } from "lucide-react";
import { cn } from "../../lib/utils";
import { SettingsMenuTrigger } from "../settings/SettingsMenuTrigger";
import {
	DropdownMenu,
	DropdownMenuCheckboxItem,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuRadioGroup,
	DropdownMenuRadioItem,
	DropdownMenuSeparator,
	DropdownMenuSub,
	DropdownMenuSubContent,
	DropdownMenuSubTrigger,
	DropdownMenuTrigger,
} from "../ui/dropdown-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import { REVIEW_MENU_LIST, REVIEW_MENU_LIST_ALIGN, REVIEW_MENU_MORE, REVIEW_MENU_PAGE, REVIEW_MENU_ROW, REVIEW_MENU_TRIGGER, REVIEW_MENU_VISIBLE_ROWS } from "./review-menu-styles";

/** Working-tree scopes. `combined` is the "Changes" row an older daemon offers instead of `uncommitted`. */
export type ReviewChangesScope = "uncommitted" | "staged" | "unstaged" | "combined";

/** The picked changes. `commits` with no SHAs is All commits. */
export type ReviewChangesValue =
	| { kind: ReviewChangesScope }
	| { kind: "commits"; shas: readonly string[] };

export type ReviewChangeStats = { additions: number; deletions: number };

export type ReviewChangesScopeOption = {
	scope: ReviewChangesScope;
	stats?: ReviewChangeStats;
	/** The scope has no changed files; its row is left out unless it is the pick. */
	empty?: boolean;
};

export type ReviewCommitRow = {
	sha: string;
	subject: string;
	additions: number;
	deletions: number;
	timestamp: string;
};

export type ReviewChangesCommits = {
	state: "loading" | "error" | "ready";
	/** Newest first, as history lists them. */
	rows: readonly ReviewCommitRow[];
	/** History stopped before the oldest commit in range. */
	truncated: boolean;
	/** `recent` lists a default branch's newest first-parent commits, so All reads "Latest N commits". */
	rangeMode?: "since_base" | "recent";
	/** The ref a since-base view compares with; named when it has no commits of its own. */
	baseRef?: string;
	allStats?: ReviewChangeStats;
	onToggleAll: () => void;
	onToggle: (sha: string) => void;
	onRetry: () => void;
};

export type ReviewChangesMenuProps = {
	/**
	 * `checked_out`: the scope rows, then Commits › as a submenu.
	 * `view`: another branch or a PR, which only has commits, so they sit at the top level.
	 */
	mode: "checked_out" | "view";
	value: ReviewChangesValue;
	/** Totals for `value`, shown on the trigger; omit while they are unknown. */
	stats?: ReviewChangeStats;
	scopes: readonly ReviewChangesScopeOption[];
	/** False hides Commits › where the checked-out branch has none to list (a scratch session, a workspace project). */
	commitsAvailable?: boolean;
	/** Called for every pick, including the selected scope, so the caller can also switch views. */
	onSelectScope: (scope: ReviewChangesScope) => void;
	commits: ReviewChangesCommits;
	className?: string;
};

const COMMIT_PAGE = REVIEW_MENU_PAGE;
/** A commit row's fixed height, so the list's box holds exactly a page. */
const COMMIT_ROW = "h-7 shrink-0";

const COMMITS_PANEL = "w-[min(20rem,calc(100vw-2rem))]";

// Checkbox rows toggle in place: Radix closes the menu on select unless the
// event is cancelled.
const keepMenuOpen = (event: Event) => event.preventDefault();

export function ReviewChangesMenu({ mode, value, stats, scopes, commitsAvailable = true, onSelectScope, commits, className }: ReviewChangesMenuProps) {
	const { t } = useTranslation();
	const [menuOpen, setMenuOpen] = useState(false);
 useLayoutEffect(()=>{if(menuOpen) markFileMenuPainted();},[menuOpen]);
	const [visibleCount, setVisibleCount] = useState(COMMIT_PAGE);
	// Whether the rows were last reached by pointer or keyboard; capture-phase
	// listeners record it before a row's own handlers move focus.
	const pointerModality = useRef(false);
	const scopeLabel = (scope: ReviewChangesScope) => {
		switch (scope) {
			case "uncommitted":
				return t("files.changes.uncommitted");
			case "staged":
				return t("files.section.staged");
			case "unstaged":
				return t("files.section.unstaged");
			case "combined":
				return t("files.reviewChanges");
		}
	};
	const recent = commits.rangeMode === "recent" && commits.state === "ready" && commits.rows.length > 0;
	const allLabel = recent ? t("files.changes.latestCommits", { count: commits.rows.length }) : t("files.changes.allCommits");
	const tickedShas = value.kind === "commits" ? value.shas : [];
	const ticked = new Set(tickedShas);
	const allTicked = value.kind === "commits" && tickedShas.length === 0;

	// Text only, like the menu's own rows.
	let triggerLabel: string;
	let triggerValue: string;
	let monoLabel = false;
	if (value.kind !== "commits") {
		triggerLabel = scopeLabel(value.kind);
		triggerValue = triggerLabel;
	} else if (tickedShas.length === 0) {
		triggerLabel = allLabel;
		triggerValue = allLabel;
	} else if (tickedShas.length === 1) {
		const sha = tickedShas[0];
		const subject = commits.rows.find((row) => row.sha === sha)?.subject;
		triggerLabel = sha.slice(0, 7);
		triggerValue = subject ? `${triggerLabel} ${subject}` : triggerLabel;
		monoLabel = true;
	} else {
		triggerLabel = t("files.changes.commitCount", { count: tickedShas.length });
		triggerValue = triggerLabel;
	}

	const emptyLabel = mode === "view" && commits.rangeMode !== "recent" && commits.baseRef
		? t("files.changes.noBranchCommits", { base: commits.baseRef })
		: t("files.noCommits");
	// A ticked commit further down than the shown page stays reachable without
	// paging to it.
	const lastTickedIndex = commits.rows.reduce((last, row, index) => (ticked.has(row.sha) ? index : last), -1);
	const shownCount = commits.rows.length <= REVIEW_MENU_VISIBLE_ROWS
		? commits.rows.length
		: Math.max(visibleCount, Math.ceil((lastTickedIndex + 1) / COMMIT_PAGE) * COMMIT_PAGE);
	const shownRows = commits.rows.slice(0, shownCount);
	const hiddenCount = commits.rows.length - shownRows.length;

	// A scope with no changes has nothing to pick, so its row goes; the current
	// pick stays, so the menu always shows what the trigger names.
	const shownScopes = scopes.filter(({ scope, empty }) => !empty || value.kind === scope);
	// With no commits to list, All commits would select an empty review, so only the reason shows.
	const noCommits = commits.state === "ready" && commits.rows.length === 0;
	const commitBlock = (
		<>
			{noCommits ? null : (
				<DropdownMenuCheckboxItem checked={allTicked} className={cn(REVIEW_MENU_ROW, COMMIT_ROW, REVIEW_MENU_LIST_ALIGN)} onCheckedChange={commits.onToggleAll} onSelect={keepMenuOpen}>
					<span className="min-w-0 flex-1 truncate">{allLabel}</span>
					{commits.allStats ? <ChangeStats muted={!allTicked} stats={commits.allStats} /> : null}
				</DropdownMenuCheckboxItem>
			)}
			{commits.state === "loading" ? (
				<DropdownMenuItem className={REVIEW_MENU_ROW} disabled>{t("files.changes.loadingCommits")}</DropdownMenuItem>
			) : commits.state === "error" ? (
				<>
					<DropdownMenuItem className={REVIEW_MENU_ROW} disabled>{t("files.changes.commitsError")}</DropdownMenuItem>
					<DropdownMenuItem
						className={REVIEW_MENU_ROW}
						onSelect={(event) => {
							event.preventDefault();
							commits.onRetry();
						}}
					>
						{t("files.retry")}
					</DropdownMenuItem>
				</>
			) : noCommits ? (
				<DropdownMenuItem className={REVIEW_MENU_ROW} disabled>{emptyLabel}</DropdownMenuItem>
			) : (
				// The box shows seven 28px rows and their 1px gaps; Show more ends the scroll.
				<div className={cn(REVIEW_MENU_LIST, "max-h-[12.625rem]")}>
					{shownRows.map((row) => (
						<CommitRow
							checked={ticked.has(row.sha)}
							key={row.sha}
							keyboardFocus={() => !pointerModality.current}
							onEscape={() => setMenuOpen(false)}
							onToggle={() => commits.onToggle(row.sha)}
							row={row}
						/>
					))}
					{hiddenCount > 0 ? (
						<DropdownMenuItem
							className={cn(REVIEW_MENU_MORE, COMMIT_ROW)}
							onSelect={(event) => {
								event.preventDefault();
								setVisibleCount(shownCount + COMMIT_PAGE);
							}}
						>
							{t("files.changes.showMore", { count: Math.min(COMMIT_PAGE, hiddenCount) })}
						</DropdownMenuItem>
					) : commits.truncated ? (
						<DropdownMenuItem className={cn(REVIEW_MENU_MORE, COMMIT_ROW)} disabled>{t("files.changes.olderNotListed")}</DropdownMenuItem>
					) : null}
				</div>
			)}
		</>
	);

	// Tracked across the whole menu: keyboard focus can reach the first commit
	// row from All commits, outside the rows' own list.
	const modalityHandlers = {
		onKeyDownCapture: () => {
			pointerModality.current = false;
		},
		onPointerMoveCapture: () => {
			pointerModality.current = true;
		},
	};
	return (
		<DropdownMenu
			onOpenChange={(open) => {
				setMenuOpen(open);
				if (open) {pointerModality.current = false;markFileViewerPerformance("file-menu-open-requested");}
				else setVisibleCount(COMMIT_PAGE);
			}}
			open={menuOpen}
		>
			<DropdownMenuTrigger asChild>
				<SettingsMenuTrigger
					aria-label={t("files.changes.trigger", { value: triggerValue })}
					className={cn(REVIEW_MENU_TRIGGER, "min-w-0 max-w-56 shrink-[0.1]", className)}
					title={triggerValue}
				>
					<span className={cn("min-w-0 truncate", monoLabel && "font-mono")}>{triggerLabel}</span>
					{stats ? <ChangeStats className="@max-[24rem]/files-header:hidden" stats={stats} /> : null}
				</SettingsMenuTrigger>
			</DropdownMenuTrigger>
			{mode === "view" ? (
				<DropdownMenuContent align="start" className={COMMITS_PANEL} {...modalityHandlers}>
					{commitBlock}
				</DropdownMenuContent>
			) : (
				<DropdownMenuContent align="start" className="w-56 max-w-[calc(100vw-2rem)]" {...modalityHandlers}>
					<DropdownMenuRadioGroup value={value.kind === "commits" ? "" : value.kind}>
						{/* Plain labels, like the trigger; the check marks what is picked. */}
						{shownScopes.map(({ scope, stats: scopeStats }) => (
							<DropdownMenuRadioItem className={REVIEW_MENU_ROW} key={scope} onSelect={() => onSelectScope(scope)} value={scope}>
								<span className="min-w-0 flex-1 truncate">{scopeLabel(scope)}</span>
								{scopeStats ? <ChangeStats stats={scopeStats} /> : null}
							</DropdownMenuRadioItem>
						))}
					</DropdownMenuRadioGroup>
					{/* A branch with no commits has nothing to list, so Commits › goes;
					    while they load it stays, showing the loading row. */}
					{commitsAvailable && !noCommits ? (
						<>
							{shownScopes.length > 0 ? <DropdownMenuSeparator /> : null}
							{/* Commits open to the side, so the scopes stay a short list and a
							    long history scrolls in its own panel. */}
							<DropdownMenuSub>
								<DropdownMenuSubTrigger className={cn(REVIEW_MENU_ROW, value.kind === "commits" && "text-foreground")}>
									<span className="min-w-0 flex-1 truncate">{t("files.commits")}</span>
									{/* The same reserved check slot as the scope rows. */}
									<span className="flex size-4 shrink-0 items-center justify-center">
										{value.kind === "commits" ? <Check aria-hidden="true" className="text-logo-accent!" /> : null}
									</span>
								</DropdownMenuSubTrigger>
								<DropdownMenuSubContent aria-label={t("files.commits")} className={COMMITS_PANEL} {...modalityHandlers}>
									{commitBlock}
								</DropdownMenuSubContent>
							</DropdownMenuSub>
						</>
					) : null}
				</DropdownMenuContent>
			)}
		</DropdownMenu>
	);
}

/**
 * A commit row whose tooltip names the commit in full, since the row truncates
 * the subject. The tooltip anchors to an inert overlay rather than the row:
 * Radix focuses a row as the pointer crosses it, and a focused tooltip trigger
 * opens at once, which would skip the hover delay. Keyboard focus opens it
 * straight away instead.
 */
function CommitRow({
	row,
	checked,
	keyboardFocus,
	onToggle,
	onEscape,
}: {
	row: ReviewCommitRow;
	checked: boolean;
	keyboardFocus: () => boolean;
	onToggle: () => void;
	onEscape: () => void;
}) {
	const [hoverOpen, setHoverOpen] = useState(false);
	const [focusOpen, setFocusOpen] = useState(false);
	return (
		<Tooltip
			onOpenChange={(open) => {
				setHoverOpen(open);
				if (!open) setFocusOpen(false);
			}}
			open={hoverOpen || focusOpen}
		>
			<DropdownMenuCheckboxItem
				checked={checked}
				className={cn(REVIEW_MENU_ROW, COMMIT_ROW)}
				onBlur={() => setFocusOpen(false)}
				onCheckedChange={onToggle}
				onFocus={() => setFocusOpen(keyboardFocus())}
				onSelect={keepMenuOpen}
			>
				<span className="min-w-0 flex-1 truncate">{row.subject}</span>
				<ChangeStats muted={!checked} stats={row} />
				<TooltipTrigger asChild>
					<span aria-hidden="true" className="absolute inset-0" />
				</TooltipTrigger>
			</DropdownMenuCheckboxItem>
			{/* An open tooltip is the top dismissable layer, so Escape would
			    otherwise only close it and leave the menu open. */}
			<TooltipContent className="max-w-80" onEscapeKeyDown={onEscape} side="left">
				<span className="line-clamp-3">{row.subject}</span>
				<span className="block font-mono text-caption">{row.sha}</span>
			</TooltipContent>
		</Tooltip>
	);
}

/**
 * `muted` greys an unticked commit's totals, so colour marks what the review
 * includes. +0 −0 says nothing, so it shows nothing; the row itself stays,
 * since a binary-only change also counts no lines.
 */
function ChangeStats({ stats, muted = false, className }: { stats: ReviewChangeStats; muted?: boolean; className?: string }) {
	if (stats.additions === 0 && stats.deletions === 0) return null;
	return (
		<span className={cn("flex shrink-0 items-center gap-1.5 text-caption tabular-nums", muted && "text-passive", className)}>
			<span className={muted ? undefined : "text-success"}>+{stats.additions}</span>
			<span className={muted ? undefined : "text-error"}>−{stats.deletions}</span>
		</span>
	);
}
