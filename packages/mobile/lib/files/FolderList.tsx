import { useCallback, useEffect, useRef, useState, type ReactElement } from "react";
import { FlatList, RefreshControl, StyleSheet, Text, View } from "react-native";
import type { ServerConfig } from "../config";
import { userFacingError } from "../connectionError";
import { refreshControlColors } from "../refreshControlColors";
import { Spinner } from "../Spinner";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { space, type } from "../tokens";
import { Button, EmptyState } from "../ui";
import { getWorkspaceTree, searchWorkspaceFiles, type WorkspaceSearchResult, type WorkspaceTreeEntry } from "./api";
import type { FileRowAction } from "./FileRowMenu.types";
import { FileRowDivider, FolderRow, PathRow } from "./rows";

type Tree = { path: string; entries: WorkspaceTreeEntry[]; truncated: boolean };

/** Folders first, then files, each alphabetical: how a file manager lists a folder. */
function sortEntries(entries: readonly WorkspaceTreeEntry[]): WorkspaceTreeEntry[] {
	return [...entries].sort((a, b) => a.type === b.type ? a.name.localeCompare(b.name) : a.type === "dir" ? -1 : 1);
}

/** One folder of the worktree, read when the page opens and again on a pull. */
export function useFolderTree(config: ServerConfig | null, sessionId: string, dir: string) {
	const [tree, setTree] = useState<Tree | null>(null);
	const [loading, setLoading] = useState(true);
	const [refreshing, setRefreshing] = useState(false);
	const [error, setError] = useState<string>();
	const request = useRef(0);

	const load = useCallback(async (quiet: boolean) => {
		if (!config) return;
		const ticket = ++request.current;
		if (quiet) setRefreshing(true);
		else setLoading(true);
		setError(undefined);
		try {
			const next = await getWorkspaceTree(config, sessionId, dir);
			if (ticket === request.current) setTree({ ...next, entries: sortEntries(next.entries) });
		} catch (cause) {
			if (ticket === request.current) setError(userFacingError(cause, "Couldn't read this folder."));
		} finally {
			if (ticket === request.current) {
				setLoading(false);
				setRefreshing(false);
			}
		}
	}, [config, dir, sessionId]);

	useEffect(() => { void load(false); }, [load]);

	return { tree, loading, refreshing, error, reload: load };
}

/** Paths across the whole worktree that match `query`, a beat after the typing stops. */
export function useWorkspaceSearch(config: ServerConfig | null, sessionId: string, query: string) {
	const [results, setResults] = useState<WorkspaceSearchResult[] | null>(null);
	const [searching, setSearching] = useState(false);
	const trimmed = query.trim();

	useEffect(() => {
		if (!config || !trimmed) { setResults(null); setSearching(false); return; }
		setSearching(true);
		let cancelled = false;
		const timer = setTimeout(() => {
			searchWorkspaceFiles(config, sessionId, trimmed)
				.then((found) => { if (!cancelled) setResults(found.results); })
				.catch(() => { if (!cancelled) setResults([]); })
				.finally(() => { if (!cancelled) setSearching(false); });
		}, 250);
		return () => { cancelled = true; clearTimeout(timer); };
	}, [config, sessionId, trimmed]);

	return { active: Boolean(trimmed), query: trimmed, results, searching };
}

type ListProps = {
	/** Drawn above the rows: the Files page's view picker. */
	header?: ReactElement | null;
	/** Room under the last row for whatever floats over the list. */
	bottomInset: number;
	onOpenFile: (path: string, changed: boolean) => void;
	onAction: (action: FileRowAction, path: string) => void;
};

/** A folder's entries, one page per folder. */
export function FolderList({ config, sessionId, dir, header, bottomInset, onOpenFile, onOpenFolder, onAction }: ListProps & {
	config: ServerConfig | null;
	sessionId: string;
	dir: string;
	onOpenFolder: (path: string) => void;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const { tree, loading, refreshing, error, reload } = useFolderTree(config, sessionId, dir);
	return (
		<FlatList
			data={loading ? [] : tree?.entries ?? []}
			keyExtractor={(item) => item.path}
			keyboardShouldPersistTaps="handled"
			ListHeaderComponent={header}
			contentContainerStyle={{ flexGrow: 1, paddingBottom: bottomInset }}
			refreshControl={<RefreshControl refreshing={refreshing} onRefresh={() => void reload(true)} {...refreshControlColors(t)} />}
			ItemSeparatorComponent={FileRowDivider}
			renderItem={({ item }) => item.type === "dir" ? (
				<FolderRow path={item.path} name={item.name} hasChanges={item.hasChanges} onPress={onOpenFolder} />
			) : (
				<PathRow path={item.path} name={item.name} status={item.status} onPress={onOpenFile} onAction={onAction} />
			)}
			ListFooterComponent={tree?.truncated ? <Text style={styles.footNote}>This folder has more entries than the daemon lists. Search for a file to reach the rest.</Text> : null}
			ListEmptyComponent={loading ? (
				<View style={styles.loading}><Spinner size="large" accessibilityLabel="Reading this folder" /></View>
			) : error ? (
				<EmptyState icon="alert-triangle" title="Couldn't read this folder" message={error} action={<Button title="Try again" icon="refresh-cw" onPress={() => void reload(false)} />} />
			) : (
				<EmptyState icon="folder" title="Empty folder" />
			)}
		/>
	);
}

/** Search results: files from anywhere in the worktree, each with its folder. */
export function SearchResults({ query, results, searching, header, bottomInset, onOpenFile, onAction }: ListProps & {
	query: string;
	results: WorkspaceSearchResult[] | null;
	searching: boolean;
}) {
	const styles = useThemedStyles(makeStyles);
	return (
		<FlatList
			data={results ?? []}
			keyExtractor={(item) => item.path}
			keyboardShouldPersistTaps="handled"
			keyboardDismissMode="on-drag"
			ListHeaderComponent={header}
			contentContainerStyle={{ flexGrow: 1, paddingBottom: bottomInset }}
			ItemSeparatorComponent={FileRowDivider}
			renderItem={({ item }) => <PathRow path={item.path} status={item.status} onPress={onOpenFile} onAction={onAction} />}
			ListEmptyComponent={searching || !results ? (
				<View style={styles.loading}><Spinner size="large" accessibilityLabel="Searching" /></View>
			) : (
				<EmptyState icon="search" title="No matching files" message={`Nothing in this worktree matches “${query}”.`} />
			)}
		/>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	loading: { paddingVertical: space.huge, alignItems: "center" },
	footNote: { ...type.footnote, color: t.textTertiary, padding: space.lg },
});
