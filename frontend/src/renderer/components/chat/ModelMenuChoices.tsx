import { Search } from "@/components/app-icons";
import { useCallback, useId, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useEdgeFade } from "../../hooks/useEdgeFade";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";

const INITIAL_VISIBLE_COUNT = 100;
const MAX_SEARCH_RESULTS = 50;
const BATCH_SIZE = 50;

export function ModelMenuChoices<T extends { id: string; label: string }>({
	models,
	selectedId,
	searchable,
	searchPlaceholder,
	children,
}: {
	models: T[];
	selectedId?: string;
	searchable?: boolean;
	searchPlaceholder?: string;
	children: (models: T[], searchActiveID?: string, optionID?: (id: string) => string, query?: string) => ReactNode;
}) {
	const { t } = useTranslation();
	const [search, setSearch] = useState("");
	const showSearch = searchable ?? models.length >= 10;
	const query = showSearch ? search.trim() : "";
	const normalizedQuery = query.toLocaleLowerCase();
	const lowered = useMemo(() => models.map((model) => model.label.toLocaleLowerCase()), [models]);
	const allMatches = useMemo(() => {
		if (!normalizedQuery) return models;
		return models.filter((_, index) => lowered[index].includes(normalizedQuery));
	}, [models, lowered, normalizedQuery]);

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

	const [loaded, setLoaded] = useState({ query: normalizedQuery, count: 0 });
	if (loaded.query !== normalizedQuery) setLoaded({ query: normalizedQuery, count: 0 });
	const visibleCount = loaded.query === normalizedQuery ? Math.max(initialLimit, loaded.count) : initialLimit;

	const matches = useMemo(
		() => (allMatches.length > visibleCount ? allMatches.slice(0, visibleCount) : allMatches),
		[allMatches, visibleCount],
	);
	const hasMore = matches.length < allMatches.length;
	const loadMore = useCallback(() => {
		if (hasMore) setLoaded({ query: normalizedQuery, count: matches.length + BATCH_SIZE });
	}, [hasMore, matches.length, normalizedQuery]);
	const [searchFocused, setSearchFocused] = useState(false);
	const searchActiveID = normalizedQuery && searchFocused ? matches[0]?.id : undefined;
	const optionIDPrefix = useId();
	const optionID = useCallback(
		(id: string) => `${optionIDPrefix}-option-${id.replace(/[^a-zA-Z0-9_-]/g, "-")}`,
		[optionIDPrefix],
	);
	const searchRef = useRef<HTMLInputElement>(null);
	const scrollRef = useRef<HTMLDivElement>(null);
	useEdgeFade(scrollRef);
	const handleScroll = useCallback(() => {
		const element = scrollRef.current;
		if (!element) return;
		if (element.scrollHeight - element.scrollTop - element.clientHeight < 150) loadMore();
	}, [loadMore]);
	const searchedQuery = useRef(normalizedQuery);
	useLayoutEffect(() => {
		if (searchedQuery.current === normalizedQuery) return;
		searchedQuery.current = normalizedQuery;
		if (scrollRef.current) scrollRef.current.scrollTop = 0;
	}, [normalizedQuery]);
	return (
		<div className="flex min-h-0 max-h-[calc(var(--size-select-menu-max)-var(--space-2)*2)] flex-col">
			{showSearch && (
				<div
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
					className="model-menu-scroll edge-fade min-h-0 overflow-y-auto overscroll-contain"
					onScroll={handleScroll}
					onKeyDownCapture={(event) => {
						if (!event.currentTarget.contains(event.target as Node)) return;
						if (event.key === "ArrowDown" && hasMore) {
							const items = event.currentTarget.querySelectorAll('[role^="menuitem"]');
							const index = Array.prototype.indexOf.call(items, event.target);
							if (index >= items.length - 10) loadMore();
						}
						if (!searchRef.current) return;
						const firstItem = event.currentTarget.querySelector('[role^="menuitem"]');
						if ((event.key === "ArrowUp" && event.target === firstItem) || (event.key === "Tab" && event.shiftKey)) {
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
			</div>
		</div>
	);
}
