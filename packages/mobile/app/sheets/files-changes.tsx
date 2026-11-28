import { useLocalSearchParams, useRouter } from "expo-router";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Platform, StyleSheet, Text } from "react-native";
import { backOr } from "../../lib/backNavigation";
import { userFacingError } from "../../lib/connectionError";
import type { WorkspaceHistory, WorkspaceManifest } from "../../lib/files/api";
import { changesId, changesOfId, changesOptions } from "../../lib/files/compareOptions";
import { selectionFromParams, viewOf, type FilesChanges, type SelectionParams } from "../../lib/files/selection";
import { useFilesBackend } from "../../lib/files/useFilesBackend";
import { useKeyedBackend } from "../../lib/files/useChangeList";
import { cachedManifest } from "../../lib/files/useWorkspace";
import { haptics } from "../../lib/haptics";
import { NativePickerList } from "../../lib/NativePickerList";
import { releaseSheetResult, takeSheetResult } from "../../lib/sheetResult";
import { Spinner } from "../../lib/Spinner";
import { HostScope } from "../../lib/store";
import type { Theme } from "../../lib/theme";
import { useThemedStyles } from "../../lib/ThemeProvider";
import { space, type } from "../../lib/tokens";
import { SheetHeader } from "../../lib/ui";

/** Which changes the Files screen lists: a worktree scope, every commit on the branch, or one. */
export default function FilesChangesSheetRoute() {
	const { hostId } = useLocalSearchParams<{ hostId?: string }>();
	return hostId ? <HostScope key={hostId} hostId={hostId}><FilesChangesSheet /></HostScope> : <FilesChangesSheet />;
}

function FilesChangesSheet() {
	const router = useRouter();
	const styles = useThemedStyles(makeStyles);
	const params = useLocalSearchParams<SelectionParams & { resultKey?: string; sessionId?: string; base?: string }>();
	const { resultKey, sessionId = "", base } = params;
	const selection = useMemo(() => selectionFromParams(params), [params]);
	const checkedOut = selection.branch.kind === "checked_out";
	const backend = useKeyedBackend(useFilesBackend().backend);
	const git = backend?.git;
	// The Files screen under the sheet has just read it.
	const [manifest, setManifest] = useState<WorkspaceManifest | null>(() => cachedManifest(backend, sessionId));
	const [history, setHistory] = useState<WorkspaceHistory>();
	const [error, setError] = useState<string>();
	// Apart from the commits' error: a history read that works says nothing about the counts.
	const [manifestError, setManifestError] = useState<string>();
	const [refreshing, setRefreshing] = useState(false);
	// Only the newest read lands: a slow first one must not undo a pull to refresh.
	const latest = useRef(0);
	useEffect(() => () => releaseSheetResult(resultKey), [resultKey]);

	const viewKey = JSON.stringify(viewOf(selection.branch) ?? null);
	/** `fresh` reads the manifest again, as a pull to refresh asks; opening uses the Files screen's. */
	const load = useCallback(async (signal?: AbortSignal, fresh = false) => {
		if (!backend || !git || !sessionId) return;
		const ticket = ++latest.current;
		const current = () => !signal?.aborted && ticket === latest.current;
		const view = JSON.parse(viewKey) as ReturnType<typeof viewOf> | null;
		const branchGit = git.at(view ?? undefined).git;
		if (!branchGit) return;
		// The worktree's scopes count what it holds, apart from the commits, so
		// neither waits on the other.
		if (checkedOut && (fresh || !cachedManifest(backend, sessionId))) {
			void backend.manifest(sessionId, signal).then(
				(next) => {
					if (!current()) return;
					setManifest(next);
					setManifestError(undefined);
				},
				(cause) => { if (current()) setManifestError(userFacingError(cause, "Couldn't refresh the working tree's counts.")); },
			);
		}
		try {
			const next = await branchGit.history(sessionId, signal);
			if (!current()) return;
			setHistory(next);
			setError(undefined);
		} catch (cause) {
			if (current()) setError(userFacingError(cause, "Couldn't list this branch's commits."));
		}
	}, [backend, checkedOut, git, sessionId, viewKey]);
	useEffect(() => {
		const controller = new AbortController();
		void load(controller.signal);
		return () => controller.abort();
	}, [load]);
	const refresh = async () => {
		setRefreshing(true);
		try { await load(undefined, true); } finally { setRefreshing(false); }
	};

	const selected = changesId(selection.changes);
	const choose = (id: string) => {
		haptics.select();
		backOr(router);
		if (id !== selected) takeSheetResult<FilesChanges>(resultKey)?.(changesOfId(id));
	};
	const options = changesOptions({ selection, manifest, history, base });
	const loading = !history && !error;
	return (
		<NativePickerList
			header={<>
				{/* iOS titles the sheet in its native bar. */}
				{Platform.OS === "ios" ? null : <SheetHeader title="Changes" />}
				{manifestError ? <Text accessibilityRole="alert" style={styles.notice}>{manifestError}</Text> : null}
				{error ? <Text accessibilityRole="alert" style={styles.notice}>{error}</Text> : null}
				{loading ? <Spinner size="large" accessibilityLabel="Loading commits" style={styles.loading} /> : null}
				{history?.commitsTruncated ? <Text style={styles.more}>Only the newest commits are listed.</Text> : null}
			</>}
			empty={history && !history.commits.length && !checkedOut ? "This branch has no commits of its own." : undefined}
			options={options}
			selected={selected}
			onSelect={choose}
			refreshing={refreshing}
			onRefresh={refresh}
		/>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	more: { fontFamily: type.footnote.fontFamily, color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	notice: { fontFamily: type.footnote.fontFamily, color: t.amber, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	loading: { marginVertical: space.lg },
});

export { SheetErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";
