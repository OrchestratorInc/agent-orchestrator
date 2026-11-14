import { markFileMenuPainted, markFileViewerPerformance } from "../../lib/file-viewer-performance";
import { useLayoutEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Check } from "@/components/app-icons";
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

export type ReviewChangesScope = "uncommitted" | "staged" | "unstaged" | "combined";

export type ReviewChangesValue =
	| { kind: ReviewChangesScope }
	| { kind: "commits"; shas: readonly string[] };

export type ReviewChangeStats = { additions: number; deletions: number };

export type ReviewChangesScopeOption = {
	scope: ReviewChangesScope;
	stats?: ReviewChangeStats;
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
	rows: readonly ReviewCommitRow[];
	truncated: boolean;
	rangeMode?: "since_base" | "recent";
	baseRef?: string;
	allStats?: ReviewChangeStats;
	onToggleAll: () => void;
	onToggle: (sha: string) => void;
	onRetry: () => void;
};

export type ReviewChangesMenuProps = {
	mode: "checked_out" | "view";
	value: ReviewChangesValue;
	stats?: ReviewChangeStats;
	scopes: readonly ReviewChangesScopeOption[];
	commitsAvailable?: boolean;
	onSelectScope: (scope: ReviewChangesScope) => void;
	commits: ReviewChangesCommits;
	className?: string;
};

const COMMIT_PAGE = REVIEW_MENU_PAGE;
const COMMIT_ROW = "h-7 shrink-0";

const COMMITS_PANEL = "w-[min(20rem,calc(100vw-2rem))]";

const keepMenuOpen = (event: Event) => event.preventDefault();

export function ReviewChangesMenu({ mode, value, stats, scopes, commitsAvailable = true, onSelectScope, commits, className }: ReviewChangesMenuProps) {
	const { t } = useTranslation();
	const [menuOpen, setMenuOpen] = useState(false);
 useLayoutEffect(()=>{if(menuOpen) markFileMenuPainted();},[menuOpen]);
	const [visibleCount, setVisibleCount] = useState(COMMIT_PAGE);
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
	const lastTickedIndex = commits.rows.reduce((last, row, index) => (ticked.has(row.sha) ? index : last), -1);
	const shownCount = commits.rows.length <= REVIEW_MENU_VISIBLE_ROWS
		? commits.rows.length
		: Math.max(visibleCount, Math.ceil((lastTickedIndex + 1) / COMMIT_PAGE) * COMMIT_PAGE);
	const shownRows = commits.rows.slice(0, shownCount);
	const hiddenCount = commits.rows.length - shownRows.length;

	const shownScopes = scopes.filter(({ scope, empty }) => !empty || value.kind === scope);
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
						{shownScopes.map(({ scope, stats: scopeStats }) => (
							<DropdownMenuRadioItem className={REVIEW_MENU_ROW} key={scope} onSelect={() => onSelectScope(scope)} value={scope}>
								<span className="min-w-0 flex-1 truncate">{scopeLabel(scope)}</span>
								{scopeStats ? <ChangeStats stats={scopeStats} /> : null}
							</DropdownMenuRadioItem>
						))}
					</DropdownMenuRadioGroup>
					{commitsAvailable && !noCommits ? (
						<>
							{shownScopes.length > 0 ? <DropdownMenuSeparator /> : null}
							<DropdownMenuSub>
								<DropdownMenuSubTrigger className={cn(REVIEW_MENU_ROW, value.kind === "commits" && "text-foreground")}>
									<span className="min-w-0 flex-1 truncate">{t("files.commits")}</span>
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
			<TooltipContent className="max-w-80" onEscapeKeyDown={onEscape} side="left">
				<span className="line-clamp-3">{row.subject}</span>
				<span className="block font-mono text-caption">{row.sha}</span>
			</TooltipContent>
		</Tooltip>
	);
}

function ChangeStats({ stats, muted = false, className }: { stats: ReviewChangeStats; muted?: boolean; className?: string }) {
	if (stats.additions === 0 && stats.deletions === 0) return null;
	return (
		<span className={cn("flex shrink-0 items-center gap-1.5 text-caption tabular-nums", muted && "text-passive", className)}>
			<span className={muted ? undefined : "text-success"}>+{stats.additions}</span>
			<span className={muted ? undefined : "text-error"}>−{stats.deletions}</span>
		</span>
	);
}
