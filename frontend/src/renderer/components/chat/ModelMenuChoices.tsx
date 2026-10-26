import { Search } from "lucide-react";
import { useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";

const MAX_SEARCH_RESULTS = 50;

/** Mounted inside menu content so closing either menu also clears its query. */
export function ModelMenuChoices<T extends { id: string; label: string }>({
	models,
	searchable,
	searchPlaceholder,
	children,
}: {
	models: T[];
	/** Whether to show the search field, ruled off from the list. Unset shows a plain one on a long list. */
	searchable?: boolean;
	/** Set where the rows are a menu that search replaces with matches; nothing is "not found" before a query. */
	searchPlaceholder?: string;
	children: (models: T[], searchActiveID?: string, optionID?: (id: string) => string, query?: string) => ReactNode;
}) {
	const { t } = useTranslation();
	const [search, setSearch] = useState("");
	const showSearch = searchable ?? models.length >= 10;
	const query = showSearch ? search.trim() : "";
	const normalizedQuery = query.toLocaleLowerCase();
	// A query can match hundreds of rows across every agent's catalog; rendering them all lagged.
	const lowered = useMemo(() => models.map((model) => model.label.toLocaleLowerCase()), [models]);
	const allMatches = useMemo(() => {
		if (!normalizedQuery) return models;
		return models.filter((_, index) => lowered[index].includes(normalizedQuery));
	}, [models, lowered, normalizedQuery]);
	const matches = useMemo(
		() => (normalizedQuery ? allMatches.slice(0, MAX_SEARCH_RESULTS) : allMatches),
		[allMatches, normalizedQuery],
	);
	const [searchFocused, setSearchFocused] = useState(false);
	const searchActiveID = normalizedQuery && searchFocused ? matches[0]?.id : undefined;
	const optionIDPrefix = useId();
	const optionID = useCallback(
		(id: string) => `${optionIDPrefix}-option-${id.replace(/[^a-zA-Z0-9_-]/g, "-")}`,
		[optionIDPrefix],
	);
	const searchRef = useRef<HTMLInputElement>(null);
	const scrollRef = useRef<HTMLDivElement>(null);
	const [canScrollDown, setCanScrollDown] = useState(false);
	const updateScrollCue = useCallback(() => {
		const element = scrollRef.current;
		setCanScrollDown(Boolean(element && element.scrollHeight - element.scrollTop > element.clientHeight + 1));
	}, []);
	// A new search starts at the top; a refreshed (polled) list keeps its scroll.
	useLayoutEffect(() => {
		if (scrollRef.current && scrollRef.current.scrollTop !== 0) scrollRef.current.scrollTop = 0;
	}, [normalizedQuery]);
	// Measured after paint: reading scrollHeight in a layout effect reflows every row per keystroke.
	useEffect(() => {
		const frame = requestAnimationFrame(updateScrollCue);
		const element = scrollRef.current;
		if (!element || typeof ResizeObserver === "undefined") return () => cancelAnimationFrame(frame);
		const observer = new ResizeObserver(updateScrollCue);
		observer.observe(element);
		return () => { cancelAnimationFrame(frame); observer.disconnect(); };
	}, [matches, updateScrollCue]);

	return (
		<div className="flex min-h-0 max-h-[calc(var(--size-select-menu-max)-var(--space-2)*2)] flex-col">
			{showSearch && (
				<div
					// No inline padding: the field then shares the menu rows' silhouette,
					// since their highlight spans the same content box.
					className={cn("relative shrink-0 pb-1", searchable ? "mb-1 border-b border-border" : undefined)}
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
			<div className="relative grid min-h-0 flex-1 grid-rows-[minmax(0,1fr)] overflow-hidden">
				<div
					ref={scrollRef}
					className="model-menu-scroll min-h-0 overflow-y-auto overscroll-contain"
					onScroll={updateScrollCue}
					onKeyDownCapture={(event) => {
						// A submenu's keys bubble here through React, not the DOM; they are its own.
						if (!searchRef.current || !event.currentTarget.contains(event.target as Node)) return;
						const firstItem = event.currentTarget.querySelector('[role^="menuitem"]');
						if ((event.key === "ArrowUp" && event.target === firstItem) || (event.key === "Tab" && event.shiftKey)) {
							// Return to search before the menu's roving focus handles the key.
							event.preventDefault();
							event.stopPropagation();
							searchRef.current.focus();
							return;
						}
						if (event.key === "Backspace") {
							event.preventDefault();
							event.stopPropagation();
							searchRef.current.focus();
							setSearch((current) => current.slice(0, -1));
							return;
						}
						if (event.key.length === 1 && event.key !== " " && !event.ctrlKey && !event.metaKey && !event.altKey) {
							// Narrow the catalog instead of letting the menu's typeahead jump
							// to whichever row starts with the typed character. Space stays
							// with the menu, where it selects the focused row.
							event.preventDefault();
							event.stopPropagation();
							searchRef.current.focus();
							setSearch((current) => current + event.key);
						}
					}}
				>
					{children(matches, searchActiveID, optionID, normalizedQuery)}
					{matches.length === 0 && (normalizedQuery || !searchPlaceholder) && (
						<p className="px-2 py-1.5 text-xs text-settings-muted">{t("settings.models.noMatches")}</p>
					)}
				</div>
				<div
					className={cn("model-menu-overflow-cue", canScrollDown ? "opacity-100" : "opacity-0")}
					aria-hidden="true"
				/>
			</div>
		</div>
	);
}
