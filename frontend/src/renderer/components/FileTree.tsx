import { createContext, forwardRef, useCallback, useContext, useEffect, useRef, useState, type HTMLAttributes, type RefObject } from "react";
import { useQueries, useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Tree, type NodeApi, type NodeRendererProps, type RowRendererProps, type TreeApi } from "react-arborist";
import { ChevronRight } from "lucide-react";
import { cn } from "../lib/utils";
import { WorkspaceEntryIcon } from "./WorkspaceEntryIcon";
import { statusLabel, statusTone } from "../lib/workspace-file-status";
import {
	buildWorkspaceFileTree,
	sessionWorkspaceTreeQueryOptions,
	type TreeNode,
	type WorkspaceTreeEntry,
} from "../hooks/useSessionWorkspaceTree";
import { sessionUiKey } from "../lib/hosts";
import { sessionWorkspaceSearchQueryOptions } from "../hooks/useSessionWorkspaceFiles";

const ROW_HEIGHT = 30;
const INDENT = 16;
const ROW_INSET = 8;

const FileTreeScrollElement = forwardRef<HTMLDivElement, HTMLAttributes<HTMLDivElement>>(
	function FileTreeScrollElement({ className, ...props }, ref) {
		return <div ref={ref} className={cn("board-scrollbar", className)} {...props} />;
	},
);

function entryToNode(entry: WorkspaceTreeEntry): TreeNode {
	if (entry.type === "dir") {
		return { name: entry.name, path: entry.path, type: "dir", hasChanges: entry.hasChanges, children: [] };
	}
	return { name: entry.name, path: entry.path, type: "file", status: entry.status, binary: entry.binary };
}

// Observe directory queries rather than copying them into local state, so
// workspace invalidation updates expanded folders without closing them.
function treeFromDirectories(entries: WorkspaceTreeEntry[], directories: Map<string, WorkspaceTreeEntry[]>): TreeNode[] {
	return entries.map((entry) => {
		const node = entryToNode(entry);
		return node.type === "dir"
			? { ...node, children: treeFromDirectories(directories.get(node.path) ?? [], directories) }
			: node;
	});
}

function useContainerSize(): [RefObject<HTMLDivElement | null>, { width: number; height: number }] {
	const ref = useRef<HTMLDivElement>(null);
	const [size, setSize] = useState({ width: 0, height: 0 });
	useEffect(() => {
		const el = ref.current;
		if (!el) return;
		const observer = new ResizeObserver(([entry]) => {
			if (!entry) return;
			const { width, height } = entry.contentRect;
			setSize({ width, height });
		});
		observer.observe(el);
		return () => observer.disconnect();
	}, []);
	return [ref, size];
}

export function FileTree({
	filterText,
	sessionId,
	hostId,
	changedOnly,
	changedOnlyData,
	selectedPath,
	onSelectPath,
	onPrefetchPath,
	flushTop = false,
}: {
	/** Start the first row at the top edge (the Files split view's divider). */
	flushTop?: boolean;
	filterText: string;
	sessionId: string;
	hostId?: string;
	changedOnly: boolean;
	changedOnlyData: TreeNode[];
	selectedPath: string | null;
	onSelectPath: (node: TreeNode) => void;
	onPrefetchPath?: (node: TreeNode) => void;
}) {
	const { t } = useTranslation();
	const treeApiRef = useRef<TreeApi<TreeNode> | null>(null);
	const uiKey = sessionUiKey(sessionId, hostId);
	const [directories, setDirectories] = useState({ key: uiKey, requested: [] as string[], open: new Set<string>() });
	const [containerRef, size] = useContainerSize();
	const normalizedFilter = filterText.trim();
	const [search, setSearch] = useState({ key: uiKey, value: normalizedFilter });
	useEffect(() => {
		const timer = setTimeout(() => setSearch({ key: uiKey, value: normalizedFilter }), 125);
		return () => clearTimeout(timer);
	}, [normalizedFilter, uiKey]);
	const searchFilter = normalizedFilter === "" ? "" : search.key === uiKey ? search.value : normalizedFilter;

	const rootQuery = useQuery({ ...sessionWorkspaceTreeQueryOptions(sessionId, "", "Unable to load workspace tree", hostId), enabled: !changedOnly && normalizedFilter.length === 0 });
	const searchQuery = useQuery({
		...sessionWorkspaceSearchQueryOptions(sessionId, searchFilter, t("files.error.searchWorkspace"), hostId),
		enabled: !changedOnly && searchFilter.length > 0,
	});

	const requested = directories.key === uiKey ? directories.requested : [];
	const directoryQueries = useQueries({
		queries: requested.map((dir) => ({
			...sessionWorkspaceTreeQueryOptions(sessionId, dir, t("files.error.loadWorkspaceTree"), hostId),
			enabled: !changedOnly && normalizedFilter.length === 0 && directories.open.has(dir),
		})),
	});
	const directoryEntries = new Map<string, WorkspaceTreeEntry[]>();
	requested.forEach((dir, index) => {
		const entries = directoryQueries[index]?.data?.entries;
		if (entries) directoryEntries.set(dir, entries);
	});
	const lazyData = treeFromDirectories(rootQuery.data?.entries ?? [], directoryEntries);

	const handleToggle = useCallback(
		(id: string) => {
			if (changedOnly) return;
			const node = treeApiRef.current?.get(id);
			if (node?.data.type !== "dir") return;
			const open = node.isOpen;
			setDirectories((previous) => {
				const current = previous.key === uiKey ? previous : { key: uiKey, requested: [], open: new Set<string>() };
				const nextOpen = new Set(current.open);
				if (open) nextOpen.add(id);
				else nextOpen.delete(id);
				return { key: uiKey, open: nextOpen, requested: current.requested.includes(id) ? current.requested : [...current.requested, id] };
			});
		},
		[changedOnly, uiKey],
	);

	const handleActivate = useCallback(
		(node: NodeApi<TreeNode>) => {
			if (node.data.type === "file") onSelectPath(node.data);
		},
		[onSelectPath],
	);

	const searchData = buildWorkspaceFileTree(searchQuery.data?.results ?? []);
	const data = changedOnly ? changedOnlyData : normalizedFilter ? searchData : lazyData;
	const isPending = !changedOnly && (normalizedFilter ? searchQuery.isPending : rootQuery.isPending);
	const activeError = !changedOnly && (normalizedFilter ? searchQuery.error : rootQuery.error);
	const isEmpty = data.length === 0 && (changedOnly || (!isPending && !activeError));

	return (
		<div className="flex h-full min-h-0 min-w-0 flex-col bg-background px-2" ref={containerRef}>
			{isPending ? (
				<p className="p-3 text-xs text-muted-foreground">{t("files.loading")}</p>
			) : null}
			{activeError ? (
				<p className="p-3 text-xs text-error">{activeError.message || t("files.error.loadWorkspaceTree")}</p>
			) : null}
			{isEmpty ? <p className="p-3 text-xs text-muted-foreground">{t("files.explorer.empty")}</p> : null}
			{size.width > 0 && size.height > 0 ? (
				<PrefetchContext.Provider value={onPrefetchPath}>
				<FlatTreeContext.Provider value={!data.some((node) => node.type === "dir")}>
					<Tree<TreeNode>
						data={data}
						ref={treeApiRef}
						idAccessor="path"
						onToggle={handleToggle}
						onActivate={handleActivate}
						openByDefault={!changedOnly && normalizedFilter.length > 0}
						selection={selectedPath ?? undefined}
						disableDrag
						disableDrop
						disableEdit
						disableMultiSelection
						searchTerm={changedOnly ? filterText : ""}
						rowHeight={ROW_HEIGHT}
						indent={INDENT}
						width={size.width}
						height={size.height}
						paddingBottom={4}
						paddingTop={flushTop ? 0 : 4}
						aria-label={t("files.explorer.tree")}
						outerElementType={FileTreeScrollElement}
						renderRow={FileTreeRowContainer}
					>
						{FileTreeRow}
					</Tree>
				</FlatTreeContext.Provider>
				</PrefetchContext.Provider>
			) : null}
		</div>
	);
}

// react-arborist's default row wrapper sets `minWidth: max-content` so a
// selection highlight never clips at the viewport edge under a long/deeply
// nested name — but that also means a row never shrinks to the panel's
// width, so name truncation (below) never has anything to truncate against
// and the whole tree scrolls horizontally instead. A file-tree sidebar
// should truncate long names with an ellipsis, not require horizontal
// scrolling to read them, so this only overrides that one property.
function FileTreeRowContainer<T>({ node, attrs, innerRef, children }: RowRendererProps<T>) {
	return (
		<div
			{...attrs}
			className="min-w-0!"
			onClick={node.handleClick}
			onFocus={(event) => event.stopPropagation()}
			ref={innerRef}
			style={{ ...attrs.style, minWidth: 0 }}
		>
			{children}
		</div>
	);
}

// A tree with no folders anywhere (e.g. a flat list of changed files) has no
// chevrons to line file icons up with, so rows drop the empty chevron slot.
const FlatTreeContext = createContext(false);
const PrefetchContext = createContext<((node: TreeNode) => void) | undefined>(undefined);

function FileTreeRow({ node, style, dragHandle }: NodeRendererProps<TreeNode>) {
	const { t } = useTranslation();
	const flat = useContext(FlatTreeContext);
	const prefetch = useContext(PrefetchContext);
	const entry = node.data;
	const isDir = entry.type === "dir";
	return (
		<div
			className={cn(
				"flex h-full min-w-0 cursor-pointer items-center gap-2 rounded-md px-2 text-[length:var(--font-size-base)] text-foreground",
				node.isSelected ? "bg-interactive-active" : "hover:bg-interactive-hover",
			)}
			onClick={() => (isDir ? node.toggle() : node.activate())}
			onPointerEnter={() => prefetch?.(entry)}
			onFocus={() => prefetch?.(entry)}
			ref={dragHandle}
			// react-arborist writes the indent as an inline paddingLeft, which
			// overrides px-2; add the row inset back so the chevron never sits
			// flush against the selection highlight.
			style={{ ...style, paddingLeft: (Number.parseFloat(String(style.paddingLeft ?? 0)) || 0) + ROW_INSET }}
		>
			{isDir ? (
				<ChevronRight
					aria-hidden="true"
					className={cn("size-3.5 shrink-0 text-passive transition-transform", node.isOpen && "rotate-90")}
				/>
			) : flat ? null : (
				<span aria-hidden="true" className="size-3.5 shrink-0" />
			)}
			{isDir ? (
				<WorkspaceEntryIcon className="size-icon-base" kind="dir" name={entry.name} testId={`folder-icon-${entry.name}`} />
			) : (
				<WorkspaceEntryIcon className="size-icon-base" kind="file" name={entry.name} testId={`file-icon-${entry.name}`} />
			)}
			<span className="min-w-0 flex-1 truncate">{entry.name}</span>
			{isDir && entry.hasChanges ? (
				<span aria-hidden="true" className="size-1.5 shrink-0 rounded-full bg-warning" />
			) : null}
			{!isDir && entry.status && entry.status !== "unmodified" ? (
				<span
					className={cn("shrink-0 text-xs font-medium", statusTone[entry.status])}
					title={t(`files.status.${entry.status}`)}
				>
					{statusLabel[entry.status]}
				</span>
			) : null}
		</div>
	);
}
