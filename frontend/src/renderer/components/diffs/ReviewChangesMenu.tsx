import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
// Aliased: @pierre/diffs also exports a FileDiff the review pane imports.
import { Check, FileDiff as FileDiffIcon, FilePen, GitCommitHorizontal, History, ListChecks, type LucideIcon } from "lucide-react";
import { cn } from "../../lib/utils";
import { formatTimeTerse } from "../../lib/format-time";
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

/** Working-tree scopes. `combined` is the "Changes" row an older daemon offers instead of `uncommitted`. */
export type ReviewChangesScope = "uncommitted" | "staged" | "unstaged" | "combined";

/** The picked changes. `commits` with no SHAs is All commits. */
export type ReviewChangesValue =
	| { kind: ReviewChangesScope }
	| { kind: "commits"; shas: readonly string[] };

export type ReviewChangeStats = { additions: number; deletions: number };

export type ReviewChangesScopeOption = { scope: ReviewChangesScope; stats?: ReviewChangeStats };

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

const COMMIT_PAGE = 50;

const scopeIcons: Record<ReviewChangesScope, LucideIcon> = {
	uncommitted: FileDiffIcon,
	staged: ListChecks,
	unstaged: FilePen,
	combined: FileDiffIcon,
};

// Checkbox rows toggle in place: Radix closes the menu on select unless the
// event is cancelled.
const keepMenuOpen = (event: Event) => event.preventDefault();

export function ReviewChangesMenu({ mode, value, stats, scopes, commitsAvailable = true, onSelectScope, commits, className }: ReviewChangesMenuProps) {
	const { t } = useTranslation();
	const [menuOpen, setMenuOpen] = useState(false);
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

	let TriggerIcon: LucideIcon;
	let triggerLabel: string;
	let triggerValue: string;
	let monoLabel = false;
	if (value.kind !== "commits") {
		TriggerIcon = scopeIcons[value.kind];
		triggerLabel = scopeLabel(value.kind);
		triggerValue = triggerLabel;
	} else if (tickedShas.length === 0) {
		TriggerIcon = History;
		triggerLabel = allLabel;
		triggerValue = allLabel;
	} else if (tickedShas.length === 1) {
		const sha = tickedShas[0];
		const subject = commits.rows.find((row) => row.sha === sha)?.subject;
		TriggerIcon = GitCommitHorizontal;
		triggerLabel = sha.slice(0, 7);
		triggerValue = subject ? `${triggerLabel} ${subject}` : triggerLabel;
		monoLabel = true;
	} else {
		TriggerIcon = GitCommitHorizontal;
		triggerLabel = t("files.changes.commitCount", { count: tickedShas.length });
		triggerValue = triggerLabel;
	}

	const emptyLabel = mode === "view" && commits.rangeMode !== "recent" && commits.baseRef
		? t("files.changes.noBranchCommits", { base: commits.baseRef })
		: t("files.noCommits");
	// A ticked commit further down than the shown page stays reachable without
	// paging to it.
	const lastTickedIndex = commits.rows.reduce((last, row, index) => (ticked.has(row.sha) ? index : last), -1);
	const shownCount = Math.max(visibleCount, Math.ceil((lastTickedIndex + 1) / COMMIT_PAGE) * COMMIT_PAGE);
	const shownRows = commits.rows.slice(0, shownCount);
	const hiddenCount = commits.rows.length - shownRows.length;

	const commitBlock = (
		<>
			<DropdownMenuCheckboxItem checked={allTicked} onCheckedChange={commits.onToggleAll} onSelect={keepMenuOpen}>
				<History aria-hidden="true" />
				<span className="min-w-0 flex-1 truncate">{allLabel}</span>
				{commits.allStats ? <ChangeStats stats={commits.allStats} /> : null}
			</DropdownMenuCheckboxItem>
			<DropdownMenuSeparator />
			{commits.state === "loading" ? (
				<DropdownMenuItem disabled>{t("files.changes.loadingCommits")}</DropdownMenuItem>
			) : commits.state === "error" ? (
				<>
					<DropdownMenuItem disabled>{t("files.changes.commitsError")}</DropdownMenuItem>
					<DropdownMenuItem
						onSelect={(event) => {
							event.preventDefault();
							commits.onRetry();
						}}
					>
						{t("files.retry")}
					</DropdownMenuItem>
				</>
			) : commits.rows.length === 0 ? (
				<DropdownMenuItem disabled>{emptyLabel}</DropdownMenuItem>
			) : (
				<div
					className="board-scrollbar flex max-h-72 flex-col gap-px overflow-y-auto pr-0.5"
					onKeyDownCapture={() => {
						pointerModality.current = false;
					}}
					onPointerMoveCapture={() => {
						pointerModality.current = true;
					}}
				>
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
							className="text-caption"
							onSelect={(event) => {
								event.preventDefault();
								setVisibleCount(shownCount + COMMIT_PAGE);
							}}
						>
							{t("files.changes.showMore", { count: Math.min(COMMIT_PAGE, hiddenCount) })}
						</DropdownMenuItem>
					) : commits.truncated ? (
						<DropdownMenuItem className="text-caption" disabled>{t("files.changes.olderNotListed")}</DropdownMenuItem>
					) : null}
				</div>
			)}
		</>
	);

	const commitsActive = value.kind === "commits";
	return (
		<DropdownMenu
			onOpenChange={(open) => {
				setMenuOpen(open);
				if (!open) setVisibleCount(COMMIT_PAGE);
			}}
			open={menuOpen}
		>
			<DropdownMenuTrigger asChild>
				<SettingsMenuTrigger
					aria-label={t("files.changes.trigger", { value: triggerValue })}
					className={cn("h-control-md min-w-0 max-w-56 shrink text-xs", className)}
					title={triggerValue}
				>
					<TriggerIcon aria-hidden="true" className="size-icon-sm shrink-0 text-passive @max-[20rem]/files-header:hidden" />
					<span className={cn("min-w-0 truncate", monoLabel && "font-mono")}>{triggerLabel}</span>
					{stats ? <ChangeStats className="@max-[24rem]/files-header:hidden" stats={stats} /> : null}
				</SettingsMenuTrigger>
			</DropdownMenuTrigger>
			{mode === "view" ? (
				<DropdownMenuContent align="start" className="w-[min(26rem,calc(100vw-2rem))]">
					{commitBlock}
				</DropdownMenuContent>
			) : (
				<DropdownMenuContent align="start" className="w-72 max-w-[calc(100vw-2rem)]">
					<DropdownMenuRadioGroup value={value.kind === "commits" ? "" : value.kind}>
						{scopes.map(({ scope, stats: scopeStats }) => {
							const Icon = scopeIcons[scope];
							return (
								<DropdownMenuRadioItem key={scope} onSelect={() => onSelectScope(scope)} value={scope}>
									<Icon aria-hidden="true" />
									<span className="min-w-0 flex-1 truncate">{scopeLabel(scope)}</span>
									{scopeStats ? <ChangeStats stats={scopeStats} /> : null}
								</DropdownMenuRadioItem>
							);
						})}
					</DropdownMenuRadioGroup>
					{commitsAvailable ? (
						<>
							<DropdownMenuSeparator />
							<DropdownMenuSub>
								<DropdownMenuSubTrigger className={cn(commitsActive && "text-foreground")}>
									<GitCommitHorizontal aria-hidden="true" />
									<span className="min-w-0 flex-1 truncate">{t("files.commits")}</span>
									{commitsActive ? (
										<span className="shrink-0 text-caption text-passive">
											{tickedShas.length === 0 ? allLabel : t("files.changes.commitCount", { count: tickedShas.length })}
										</span>
									) : null}
									<span className="flex size-4 shrink-0 items-center justify-center">
										{commitsActive ? <Check aria-hidden="true" className="text-logo-accent!" /> : null}
									</span>
								</DropdownMenuSubTrigger>
								<DropdownMenuSubContent className="w-[min(26rem,calc(100vw-2rem))]">{commitBlock}</DropdownMenuSubContent>
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
				className="gap-2"
				onBlur={() => setFocusOpen(false)}
				onCheckedChange={onToggle}
				onFocus={() => setFocusOpen(keyboardFocus())}
				onSelect={keepMenuOpen}
			>
				<span className="min-w-0 flex-1 truncate">{row.subject}</span>
				<ChangeStats stats={row} />
				<span className="shrink-0 text-caption text-passive">{formatTimeTerse(row.timestamp)}</span>
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

function ChangeStats({ stats, className }: { stats: ReviewChangeStats; className?: string }) {
	return (
		<span className={cn("flex shrink-0 items-center gap-1.5 text-caption tabular-nums", className)}>
			<span className="text-success">+{stats.additions}</span>
			<span className="text-error">−{stats.deletions}</span>
		</span>
	);
}
