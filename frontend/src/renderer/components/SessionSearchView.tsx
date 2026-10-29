import { ArrowUpRight, Loader2, Search } from "lucide-react";
import { ScrollArea } from "radix-ui";
import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "@tanstack/react-router";
import { formatTimeCompact } from "../lib/format-time";
import { LOCAL_HOST } from "../lib/hosts";
import { sessionNavigateTarget } from "../lib/navigate-to-session";
import { revealSessionSearchMatch } from "../lib/session-search-reveal";
import { hidesShellTopbar } from "../lib/platform";
import { cn } from "../lib/utils";
import { useWorkspaceQuery } from "../hooks/useWorkspaceQuery";
import {
	SESSION_SEARCH_MIN_QUERY_CHARS,
	SESSION_SEARCH_PAGE_SIZE,
	snippetSegments,
	useSessionSearch,
	type SessionSearchCoverage,
	type SessionSearchFilters,
	type SessionSearchResult,
} from "../hooks/useSessionSearch";
import { TopbarButton, topbarProjectLabelClass } from "./TopbarButton";
import { Input } from "./ui/input";
import { SettingsOptionMenu } from "./settings/SettingsOptionMenu";

const ALL = "all";

/**
 * A match the search view hands to the session it opens.
 *
 * The session view passes this into the Chat timeline's existing annotation
 * navigation, which is what scrolls to and highlights the matched message. A
 * native-transcript result carries no message id, so it opens the session
 * without pretending to scroll.
 */
export type SessionSearchOpenTarget = {
	sessionId: string;
	projectId: string;
	messageId: string;
	/** The matched text, used to highlight the span inside the message body. */
	text: string;
};

/**
 * Search every past session by title, message text, and touched file paths.
 *
 * It lives beside the board rather than inside a command palette: past sessions
 * are a browsable archive, not an action to run.
 */
export function SessionSearchView() {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const showPageTitle = hidesShellTopbar();
	const workspaces = useWorkspaceQuery({ includeCloud: false }).data ?? [];
	const [query, setQuery] = useState("");
	const [projectId, setProjectId] = useState(ALL);
	const [harness, setHarness] = useState(ALL);
	const [from, setFrom] = useState("");
	const [to, setTo] = useState("");
	const [sort, setSort] = useState<"relevance" | "recent">("relevance");
	// Paging is a growing limit rather than an offset: each page appends, so the
	// list reads as one result set and an earlier page cannot be invalidated by
	// new sessions arriving between requests.
	const [limit, setLimit] = useState(SESSION_SEARCH_PAGE_SIZE);
	const inputRef = useRef<HTMLInputElement>(null);

	// The field is the point of the view, so it takes focus on arrival.
	useEffect(() => {
		inputRef.current?.focus();
	}, []);

	const filters: SessionSearchFilters = useMemo(
		() => ({
			query,
			projectId: projectId === ALL ? "" : projectId,
			harness: harness === ALL ? "" : harness,
			from,
			to,
			sort,
			limit,
		}),
		[from, harness, limit, projectId, query, sort, to],
	);
	const search = useSessionSearch(filters);
	const results = search.data?.results ?? [];
	const coverage = search.data?.coverage;
	const total = search.data?.total ?? results.length;
	const hasMore = results.length < total;
	const trimmed = query.trim();
	const tooShort = trimmed.length > 0 && trimmed.length < SESSION_SEARCH_MIN_QUERY_CHARS;

	const projectOptions = useMemo(
		() => [
			{ value: ALL, label: t("search.allProjects") },
			...workspaces.map((workspace) => ({ value: workspace.id, label: workspace.name })),
		],
		[t, workspaces],
	);
	const harnessOptions = useMemo(() => {
		const seen = new Set<string>();
		for (const workspace of workspaces) {
			for (const session of workspace.sessions) {
				if (session.provider) seen.add(session.provider);
			}
		}
		if (coverage) for (const name of coverage.unindexedHarnesses ?? []) seen.add(name);
		return [
			{ value: ALL, label: t("search.allHarnesses") },
			...[...seen].sort().map((name) => ({ value: name, label: name })),
		];
	}, [coverage, t, workspaces]);

	const open = (result: SessionSearchResult) => {
		// Hand the match to the session before navigating: the Chat timeline is
		// mounted by the shell, so it picks this up when it renders rather than
		// from route state.
		revealSessionSearchMatch({
			sessionId: result.sessionId,
			messageId: result.messageId,
			snippet: result.snippet,
			highlights: result.highlights,
			query: filters.query,
			reviewId: result.reviewId,
		});
		const target = sessionNavigateTarget(
			result.projectId || undefined,
			result.sessionId,
			LOCAL_HOST,
		);
		void navigate(target);
	};

	// A new question starts at the first page; keeping a grown limit would show
	// a different result count than the one just asked for.
	useEffect(() => {
		setLimit(SESSION_SEARCH_PAGE_SIZE);
	}, [query, projectId, harness, from, to, sort]);

	const clearFilters = () => {
		setProjectId(ALL);
		setHarness(ALL);
		setFrom("");
		setTo("");
	};
	const filtersActive = projectId !== ALL || harness !== ALL || from !== "" || to !== "";

	return (
		<div className="flex min-h-0 flex-1 flex-col bg-background">
			<header className="workspace-topbar-container center-panel-titlebar flex h-toolbar shrink-0 items-center justify-between gap-3 border-b border-border-strong pr-[calc((var(--size-toolbar)-var(--size-topbar-control))/2)]">
				<h1 className={showPageTitle ? cn(topbarProjectLabelClass, "inline-flex items-center gap-1.5") : "sr-only"}>
					{showPageTitle ? <Search className="size-icon-md" aria-hidden="true" /> : null}
					{t("search.title")}
				</h1>
				<div className="workspace-topbar-actions flex shrink-0 items-center gap-[calc((var(--size-toolbar)-var(--size-topbar-control))/2)]">
					{/* Relevance while searching, recency while browsing: the same
					    two-mode control Orca and Linear offer. */}
					<SettingsOptionMenu
						aria-label={t("search.sort")}
						value={sort}
						options={[
							{ value: "relevance", label: t("search.sortRelevance") },
							{ value: "recent", label: t("search.sortRecent") },
						]}
						onChange={(next) => setSort(next as "relevance" | "recent")}
						menuAlign="end"
						triggerClassName="h-(--size-control-md)! text-xs!"
					/>
					{filtersActive ? (
						<TopbarButton onClick={clearFilters}>{t("search.clearFilters")}</TopbarButton>
					) : null}
				</div>
			</header>

			<div className="flex shrink-0 flex-col gap-2 border-b border-border px-4 py-3">
				<label className="relative block">
					<Search aria-hidden="true" className="pointer-events-none absolute start-2.5 top-1/2 size-icon-sm -translate-y-1/2 text-passive" />
					<Input
						ref={inputRef}
						aria-label={t("search.fieldLabel")}
						className="inspector-field-input ps-8 [&::-webkit-search-cancel-button]:appearance-none"
						onChange={(event) => setQuery(event.target.value)}
						placeholder={t("search.placeholder")}
						type="search"
						value={query}
					/>
				</label>
				<div className="flex flex-wrap items-center gap-2">
					<SettingsOptionMenu
						aria-label={t("search.projectFilter")}
						value={projectId}
						options={projectOptions}
						onChange={setProjectId}
						menuAlign="start"
						menuClassName="max-h-72"
						triggerClassName="h-(--size-control-md)! text-xs!"
					/>
					<SettingsOptionMenu
						aria-label={t("search.harnessFilter")}
						value={harness}
						options={harnessOptions}
						onChange={setHarness}
						menuAlign="start"
						menuClassName="max-h-72"
						triggerClassName="h-(--size-control-md)! text-xs!"
					/>
					<label className="flex items-center gap-1.5 text-xs text-muted-foreground">
						<span>{t("search.from")}</span>
						<Input
							aria-label={t("search.from")}
							className="inspector-field-input h-(--size-control-md)! w-36 text-xs!"
							onChange={(event) => setFrom(event.target.value)}
							type="date"
							value={from}
						/>
					</label>
					<label className="flex items-center gap-1.5 text-xs text-muted-foreground">
						<span>{t("search.to")}</span>
						<Input
							aria-label={t("search.to")}
							className="inspector-field-input h-(--size-control-md)! w-36 text-xs!"
							onChange={(event) => setTo(event.target.value)}
							type="date"
							value={to}
						/>
					</label>
				</div>
			</div>

			<ScrollArea.Root asChild type="hover" scrollHideDelay={500}>
				<main className="relative min-h-0 flex-1 overflow-hidden">
					<ScrollArea.Viewport className="h-full w-full overscroll-contain focus-visible:outline-none">
						<div className="p-4">
							{tooShort ? (
								<p role="status" className="px-3 py-8 text-center text-sm text-muted-foreground">
									{t("search.tooShort", { count: SESSION_SEARCH_MIN_QUERY_CHARS })}
								</p>
							) : trimmed.length === 0 ? (
								<div className="flex flex-col items-center gap-1 py-16 text-center">
									<p className="text-control font-medium text-foreground">{t("search.emptyTitle")}</p>
									<p className="max-w-sm text-xs text-muted-foreground">{t("search.emptyDescription")}</p>
								</div>
							) : search.isLoading ? (
								<p role="status" className="flex items-center justify-center gap-2 px-3 py-8 text-sm text-muted-foreground">
									<Loader2 aria-hidden="true" className="size-icon-sm animate-spin motion-reduce:animate-none" />
									{t("search.loading")}
								</p>
							) : search.error ? (
								<p role="alert" className="px-3 py-8 text-center text-sm text-destructive">{search.error.message}</p>
							) : results.length === 0 ? (
								<div className="flex flex-col items-center gap-1 py-16 text-center">
									<p className="text-control font-medium text-foreground">{t("search.noMatches")}</p>
									<CoverageNote coverage={coverage} />
								</div>
							) : (
								<>
									{/* How many matched, not just how many are on screen. */}
									<p className="px-3 pb-2 text-xs text-muted-foreground" data-testid="session-search-count">
										{t("search.resultCount", { shown: results.length, total })}
									</p>
									<ul className="divide-y divide-border" data-testid="session-search-results">
										{results.map((result) => (
											<SearchResultRow
												key={`${result.sessionId}:${result.messageId}:${result.seq}:${result.kind}`}
												result={result}
												onOpen={() => open(result)}
											/>
										))}
									</ul>
									{hasMore ? (
										<div className="flex justify-center pt-3">
											<TopbarButton onClick={() => setLimit((current) => current + SESSION_SEARCH_PAGE_SIZE)}>
												{t("search.showMore", { remaining: total - results.length })}
											</TopbarButton>
										</div>
									) : null}
									<CoverageNote coverage={coverage} className="px-3 pt-4" />
								</>
							)}
						</div>
					</ScrollArea.Viewport>
					<ScrollArea.Scrollbar orientation="vertical" className="z-10 flex w-2 touch-none select-none p-0.5">
						<ScrollArea.Thumb className="relative flex-1 rounded-full bg-[color-mix(in_srgb,var(--color-scrollbar)_42%,transparent)] hover:bg-[color-mix(in_srgb,var(--color-scrollbar)_56%,transparent)]" />
					</ScrollArea.Scrollbar>
				</main>
			</ScrollArea.Root>
		</div>
	);
}

function SearchResultRow({ result, onOpen }: { result: SessionSearchResult; onOpen: () => void }) {
	const { t } = useTranslation();
	const segments = snippetSegments(result.snippet, result.highlights);
	const sessionName = result.sessionName || result.sessionId;
	const projectName = result.projectName || result.projectId;
	const stateLabel = result.sessionState === "terminated"
		? t("search.stateTerminated")
		: result.sessionState === "hibernated"
			? t("search.stateHibernated")
			: undefined;

	return (
		<li className="py-0.5">
			<button
				type="button"
				className="flex w-full flex-col gap-1 rounded-lg px-3 py-2.5 text-left outline-none hover:bg-interactive-hover focus-visible:bg-interactive-hover"
				onClick={onOpen}
			>
				<span className="flex min-w-0 items-center gap-2">
					<span className="min-w-0 shrink truncate text-control font-medium text-foreground">{sessionName}</span>
					{stateLabel ? (
						<span className="shrink-0 rounded-full border border-border px-1.5 text-[11px] text-muted-foreground">{stateLabel}</span>
					) : null}
					{result.reviewId ? (
						<span className="shrink-0 text-xs text-muted-foreground">{t("terminal.reviewer")}</span>
					) : null}
					<span className="min-w-0 shrink truncate text-xs text-muted-foreground">{projectName}</span>
					<span className="shrink-0 text-xs text-passive">{result.harness}</span>
					<time className="ms-auto shrink-0 text-xs tabular-nums text-passive" dateTime={result.createdAt}>
						{formatTimeCompact(result.createdAt, { longUnits: true })}
					</time>
					<ArrowUpRight aria-hidden="true" className="size-icon-sm shrink-0 text-passive" />
				</span>
				<span className="line-clamp-2 text-xs leading-relaxed text-muted-foreground" data-testid="session-search-snippet">
					{segments.map((segment, index) =>
						segment.match ? (
							<mark key={index} className="rounded-sm bg-status-attention/25 px-0.5 text-foreground">
								{segment.text}
							</mark>
						) : (
							<span key={index}>{segment.text}</span>
						),
					)}
				</span>
				{/* Each session is listed once, at its best-matching message. The count
				    is how many of its messages matched; the opened session's find bar
				    steps through them. */}
				{(result.sessionMatches ?? 0) > 0 ? (
					<span className="text-xs text-passive" data-testid="session-search-match-count">
						{t("search.matchCount", { count: result.sessionMatches ?? 0 })}
					</span>
				) : null}
			</button>
		</li>
	);
}

/**
 * Says what the index cannot see. An empty result over an incomplete index is
 * not the same answer as an empty result over a complete one.
 */
function CoverageNote({ coverage, className }: { coverage?: SessionSearchCoverage; className?: string }) {
	const { t } = useTranslation();
	if (!coverage) return null;
	const harnesses = coverage.unindexedHarnesses ?? [];
	if (coverage.state !== "backfilling" && harnesses.length === 0) return null;
	return (
		<div className={cn("flex flex-col gap-1 text-center text-xs text-muted-foreground", className)}>
			{coverage.state === "backfilling" ? (
				<p>{t("search.coverageBackfilling", { count: coverage.pendingSources })}</p>
			) : null}
			{harnesses.length > 0 ? (
				<p>{t("search.coverageUnindexedHarnesses", { harnesses: harnesses.join(", ") })}</p>
			) : null}
		</div>
	);
}
