import { Search } from "@/components/app-icons";
import { useCallback, useId, useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { useEdgeFade } from "../../hooks/useEdgeFade";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";

const INITIAL_VISIBLE_COUNT = 100;
const MAX_SEARCH_RESULTS = 50;
const BATCH_SIZE = 50;

/** Mounted inside menu content so closing either menu also clears its query. */
export function ModelMenuChoices<T extends { id: string; label: string }>({
	models,
	selectedId,
	searchable,
	searchPlaceholder,
	browse,
	browseKey,
	rows,
	header,
	footer,
	className,
	children,
}: {
	models: T[];
	/** The currently active or selected model, ensuring it is included in initial visible rows if known. */
	selectedId?: string;
	/** Whether to show the search field, ruled off from the list. Unset shows a plain one on a long list. */
	searchable?: boolean;
	/** Set where the rows are a menu that search replaces with matches; nothing is "not found" before a query. */
	searchPlaceholder?: string;
	/** The rows listed while nothing is typed, where search reaches more than them (every agent's models). */
	browse?: T[];
	/** Names what `browse` lists (the chosen agent), so a new list opens at its top. */
	browseKey?: string;
	/**
	 * Rows to show before the list scrolls, so a long catalog does not stretch the menu.
	 * Unset takes the pane's own max height. The count includes any rows above the list, such as favourites.
	 */
	rows?: number;
	/**
	 * Controls between the search and the list, such as the agent rail. Up and Shift+Tab
	 * leave them for the search, Down and Tab for the list, and typing goes to the search.
	 */
	header?: (controls: { clearSearch: () => void }) => ReactNode;
	/** Rows under the list, kept in view while it scrolls, and hidden while searching. */
	footer?: ReactNode;
	className?: string;
	children: (models: T[], searchActiveID?: string, optionID?: (id: string) => string, query?: string) => ReactNode;
}) {
	const { t } = useTranslation();
	const [search, setSearch] = useState("");
	const showSearch = searchable ?? models.length >= 10;
	const query = showSearch ? search.trim() : "";
	const normalizedQuery = query.toLocaleLowerCase();
	// A query or large catalog can contain hundreds of rows (e.g. OpenCode has 500+ models);
	// rendering them all at once freezes the menu on open. Window initial render and append on scroll.
	const lowered = useMemo(() => models.map((model) => model.label.toLocaleLowerCase()), [models]);
	const allMatches = useMemo(() => {
		if (!normalizedQuery) return browse ?? models;
		return models.filter((_, index) => lowered[index].includes(normalizedQuery));
	}, [browse, models, lowered, normalizedQuery]);

	const initialLimit = useMemo(() => {
		if (normalizedQuery) return MAX_SEARCH_RESULTS;
		if (selectedId) {
			const index = allMatches.findIndex((m) => m.id === selectedId);
			if (index >= INITIAL_VISIBLE_COUNT) {
				return Math.min(index + 10, allMatches.length);
			}
		}
		return INITIAL_VISIBLE_COUNT;
	}, [normalizedQuery, selectedId, allMatches]);

	// Which list is shown: a query's matches, or the browsed rows.
	const listKey = normalizedQuery ? `query:${normalizedQuery}` : `browse:${browseKey ?? ""}`;
	// Rows loaded past the initial window, per list. Reset while rendering, not in an
	// effect, so a new query never commits the previous query's longer list first.
	const [loaded, setLoaded] = useState({ list: listKey, count: 0 });
	if (loaded.list !== listKey) setLoaded({ list: listKey, count: 0 });
	const visibleCount = loaded.list === listKey ? Math.max(initialLimit, loaded.count) : initialLimit;

	const matches = useMemo(
		() => (allMatches.length > visibleCount ? allMatches.slice(0, visibleCount) : allMatches),
		[allMatches, visibleCount],
	);
	const hasMore = matches.length < allMatches.length;
	// From the rendered length, so several scroll events before a commit add one batch, not several.
	const loadMore = useCallback(() => {
		if (hasMore) setLoaded({ list: listKey, count: matches.length + BATCH_SIZE });
	}, [hasMore, matches.length, listKey]);
	const [searchFocused, setSearchFocused] = useState(false);
	const searchActiveID = normalizedQuery && searchFocused ? matches[0]?.id : undefined;
	const optionIDPrefix = useId();
	const optionID = useCallback(
		(id: string) => `${optionIDPrefix}-option-${id.replace(/[^a-zA-Z0-9_-]/g, "-")}`,
		[optionIDPrefix],
	);
	const searchRef = useRef<HTMLInputElement>(null);
	const scrollRef = useRef<HTMLDivElement>(null);
	const headerRef = useRef<HTMLDivElement>(null);
	const footerRef = useRef<HTMLDivElement>(null);
	// A row is the menu item's text line plus its py-1.5, with the surface's 1px gap between rows.
	// The pane starts at that height whatever it lists: a search with no matches drops to a single
	// line, and the menu must not shrink under the pointer while it is typed in. It only grows into
	// room the held height below leaves it.
	const rowsHeight = rows === undefined ? undefined : `calc((var(--line-height-row) + var(--space-1_5) * 2) * ${rows} + 1px * ${rows})`;
	const rowCap = rowsHeight === undefined ? undefined : { flex: `1 1 ${rowsHeight}`, minHeight: rowsHeight };
	const rootRef = useRef<HTMLDivElement>(null);
	const holdsHeight = rows !== undefined;
	// While open, the menu never gets shorter: when the rows under the list shrink (another agent
	// has fewer settings), the list takes the room instead of the menu's edge moving under the pointer.
	useLayoutEffect(() => {
		const root = rootRef.current;
		if (!holdsHeight || !root || typeof ResizeObserver === "undefined") return;
		let tallest = 0;
		const observer = new ResizeObserver(() => {
			if (root.offsetHeight <= tallest) return;
			tallest = root.offsetHeight;
			root.style.minHeight = `${tallest}px`;
		});
		observer.observe(root);
		return () => {
			observer.disconnect();
			root.style.minHeight = "";
		};
	}, [holdsHeight]);
	useEdgeFade(scrollRef);
	const clearSearch = useCallback(() => setSearch(""), []);
	// The header's own tab stop (the rail's chosen agent), if it has one.
	const focusHeader = () => {
		const target = headerRef.current?.querySelector<HTMLElement>('[tabindex="0"]');
		target?.focus();
		return Boolean(target);
	};
	// Into the list on its checked row, else its first enabled one; a list with none goes on to
	// the footer's first enabled row. A disabled row is never the landing spot.
	const focusList = () => {
		const scroller = scrollRef.current;
		(scroller?.querySelector<HTMLElement>('[role="menuitemradio"][aria-checked="true"]:not([data-disabled])')
			?? scroller?.querySelector<HTMLElement>('[role^="menuitem"]:not([data-disabled])')
			?? footerRef.current?.querySelector<HTMLElement>('[role^="menuitem"]:not([data-disabled])'))?.focus();
	};
	const typeIntoSearch = (event: KeyboardEvent) => {
		if (!searchRef.current) return false;
		if (event.key === "Backspace") {
			event.preventDefault();
			searchRef.current.focus();
			setSearch((current) => current.slice(0, -1));
			return true;
		}
		if (event.key.length === 1 && event.key !== " " && !event.ctrlKey && !event.metaKey && !event.altKey) {
			// Narrow the catalog instead of letting the menu's typeahead jump
			// to whichever row starts with the typed character. Space stays
			// with the menu, where it selects the focused row.
			event.preventDefault();
			searchRef.current.focus();
			setSearch((current) => current + event.key);
			return true;
		}
		return false;
	};
	const handleScroll = useCallback(() => {
		const element = scrollRef.current;
		if (!element) return;
		if (element.scrollHeight - element.scrollTop - element.clientHeight < 150) loadMore();
	}, [loadMore]);
	// A new search or browsed list starts at the top; a refreshed (polled) list keeps its
	// scroll. Not on mount: the list opens at the top, and reading scrollTop then forces a
	// style pass over the whole page while the menu is still being inserted.
	const shownList = useRef(listKey);
	useLayoutEffect(() => {
		if (shownList.current === listKey) return;
		shownList.current = listKey;
		if (scrollRef.current) scrollRef.current.scrollTop = 0;
	}, [listKey]);
	return (
		<div ref={rootRef} className={cn("flex min-h-0 max-h-[calc(var(--size-select-menu-max)-var(--space-2)*2)] flex-col", className)}>
			{showSearch && (
				<div
					// No inline padding: the field then shares the menu rows' silhouette,
					// since their highlight spans the same content box.
					className={cn("relative shrink-0 pb-1", searchable && !header ? "mb-1 border-b border-border" : undefined)}
					onClick={(event) => event.stopPropagation()}
					onKeyDown={(event) => {
						if (event.nativeEvent.isComposing) {
							event.stopPropagation();
							return;
						}
						if (event.key === "Escape") return;
						event.stopPropagation();
						if (event.key === "Enter") {
							event.preventDefault();
							if (!normalizedQuery) return;
							const highlighted = searchActiveID
								? document.getElementById(optionID(searchActiveID))
								: null;
							(
								highlighted ??
								scrollRef.current?.querySelector<HTMLElement>('[role="menuitemradio"]')
							)?.click();
							return;
						}
						if (event.key === "ArrowDown" && !normalizedQuery && focusHeader()) {
							event.preventDefault();
							return;
						}
						if (event.key === "ArrowDown" || event.key === "ArrowUp") {
							event.preventDefault();
							const items = scrollRef.current?.querySelectorAll<HTMLElement>('[role^="menuitem"]');
							const target = event.key === "ArrowDown" ? items?.[0] : items?.[items.length - 1];
							target?.focus();
						}
					}}
				>
					<div className="relative">
						<Search
							className="pointer-events-none absolute left-3 top-1/2 size-icon-sm -translate-y-1/2 text-settings-muted"
							aria-hidden="true"
						/>
						<input
							ref={searchRef}
							type="search"
							aria-label={t("settings.models.searchAria", { label: "models" })}
							aria-activedescendant={searchActiveID ? optionID(searchActiveID) : undefined}
							value={search}
							onChange={(event) => setSearch(event.target.value)}
							onFocus={() => setSearchFocused(true)}
							onBlur={() => setSearchFocused(false)}
							placeholder={searchPlaceholder ?? t("settings.models.searchPlaceholder")}
							className="menu-search-input h-auto! rounded-[10px] py-[7px]! pl-8! text-xs!"
						/>
					</div>
					{normalizedQuery ? (
						<span className="sr-only" aria-live="polite">
							{t("settings.models.matchingCount", { visible: matches.length, total: models.length })}
						</span>
					) : null}
				</div>
			)}
			{header ? (
				<div
					ref={headerRef}
					className="shrink-0 border-b border-border pb-1 mb-1"
					// Kept from the composer's click-to-focus handler, as the search is.
					onClick={(event) => event.stopPropagation()}
					onKeyDown={(event) => {
						if (event.nativeEvent.isComposing || event.key === "Escape") return;
						// The header's keys are its own, never the menu's roving focus or typeahead.
						event.stopPropagation();
						if (event.key === "ArrowUp" || (event.key === "Tab" && event.shiftKey)) {
							event.preventDefault();
							searchRef.current?.focus();
							return;
						}
						if (event.key === "ArrowDown" || event.key === "Tab") {
							event.preventDefault();
							focusList();
							return;
						}
						typeIntoSearch(event);
					}}
				>
					{header({ clearSearch })}
				</div>
			) : null}
			<div className="relative grid min-h-0 flex-1 grid-rows-[minmax(0,1fr)] overflow-hidden" style={rowCap}>
				<div
					ref={scrollRef}
					className="model-menu-scroll edge-fade min-h-0 overflow-y-auto overscroll-contain"
					onScroll={handleScroll}
					onKeyDownCapture={(event) => {
						// A submenu's keys bubble here through React, not the DOM; they are its own.
						if (!event.currentTarget.contains(event.target as Node)) return;
						if (event.key === "ArrowDown" && hasMore) {
							// Load the next batch only as focus nears the last rendered row.
							const items = event.currentTarget.querySelectorAll('[role^="menuitem"]');
							const index = Array.prototype.indexOf.call(items, event.target);
							if (index >= items.length - 10) loadMore();
						}
						if (!searchRef.current) return;
						const firstItem = event.currentTarget.querySelector('[role^="menuitem"]');
						if ((event.key === "ArrowUp" && event.target === firstItem) || (event.key === "Tab" && event.shiftKey)) {
							// Back to the header, else the search, before the menu's roving focus handles the key.
							event.preventDefault();
							event.stopPropagation();
							if (!focusHeader()) searchRef.current.focus();
							return;
						}
						if (typeIntoSearch(event)) event.stopPropagation();
					}}
				>
					{children(matches, searchActiveID, optionID, normalizedQuery)}
					{matches.length === 0 && (normalizedQuery || !searchPlaceholder) && (
						<p className="px-2 py-1.5 text-xs text-settings-muted">{t("settings.models.noMatches")}</p>
					)}
				</div>
			</div>
			{/* Hiding the footer during a search only frees room for the list while that list grows
			    with its results. A capped list gains nothing, and the menu would lose a row the
			    moment a key is typed, so the footer stays put. */}
			{footer && (!normalizedQuery || rows !== undefined) ? (
				<div
					ref={footerRef}
					className="shrink-0"
					onKeyDownCapture={(event) => {
						// A submenu's keys bubble here through React, not the DOM; they are its own.
						if (!event.currentTarget.contains(event.target as Node)) return;
						const enabled = '[role^="menuitem"]:not([data-disabled])';
						// Up from the first row goes to the list's last row through the menu's own focus,
						// unless the list has none; Shift+Tab leaves for the header, as it does from the list.
						const upward = event.key === "ArrowUp"
							&& event.target === event.currentTarget.querySelector(enabled)
							&& !scrollRef.current?.querySelector(enabled);
						if (!upward && !(event.key === "Tab" && event.shiftKey)) return;
						event.preventDefault();
						event.stopPropagation();
						if (!focusHeader()) searchRef.current?.focus();
					}}
				>
					{footer}
				</div>
			) : null}
		</div>
	);
}
