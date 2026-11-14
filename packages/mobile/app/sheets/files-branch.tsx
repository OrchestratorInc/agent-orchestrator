import { useLocalSearchParams, useRouter } from "expo-router";
import { useCallback, useEffect, useRef, useState } from "react";
import { Platform, StyleSheet, Text } from "react-native";
import { backOr } from "../../lib/backNavigation";
import { ComposerSearchField } from "../../lib/chat/ComposerSearchField";
import { userFacingError } from "../../lib/connectionError";
import type { WorkspaceBranches } from "../../lib/files/api";
import { branchOptions } from "../../lib/files/compareOptions";
import { branchId, branchOfRow, type FilesBranch } from "../../lib/files/selection";
import { useFilesBackend } from "../../lib/files/useFilesBackend";
import { useKeyedBackend } from "../../lib/files/useChangeList";
import { haptics } from "../../lib/haptics";
import { NativePickerList } from "../../lib/NativePickerList";
import { releaseSheetResult, takeSheetResult } from "../../lib/sheetResult";
import { Spinner } from "../../lib/Spinner";
import { HostScope } from "../../lib/store";
import type { Theme } from "../../lib/theme";
import { useThemedStyles } from "../../lib/ThemeProvider";
import { space, type } from "../../lib/tokens";
import { SheetHeader } from "../../lib/ui";

/** A list this long gets a search field. */
const SEARCH_FROM = 8;

/** Which branch the Files screen compares: the checked-out one, another, or the session's pull request. */
export default function FilesBranchSheetRoute() {
	const { hostId } = useLocalSearchParams<{ hostId?: string }>();
	return hostId ? <HostScope key={hostId} hostId={hostId}><FilesBranchSheet /></HostScope> : <FilesBranchSheet />;
}

function FilesBranchSheet() {
	const router = useRouter();
	const styles = useThemedStyles(makeStyles);
	const { resultKey, sessionId = "", selected = "checked_out" } = useLocalSearchParams<{ resultKey?: string; sessionId?: string; selected?: string }>();
	const git = useKeyedBackend(useFilesBackend().backend)?.git;
	const [list, setList] = useState<WorkspaceBranches>();
	const [error, setError] = useState<string>();
	const [refreshing, setRefreshing] = useState(false);
	const [query, setQuery] = useState("");
	// Only the newest read lands: a slow first one must not undo a pull to refresh.
	const latest = useRef(0);
	useEffect(() => () => releaseSheetResult(resultKey), [resultKey]);

	const load = useCallback(async (signal?: AbortSignal) => {
		if (!git || !sessionId) return;
		const ticket = ++latest.current;
		try {
			const next = await git.branches(sessionId, signal);
			if (signal?.aborted || ticket !== latest.current) return;
			setList(next);
			setError(undefined);
		} catch (cause) {
			if (!signal?.aborted && ticket === latest.current) setError(userFacingError(cause, "Couldn't list this session's branches."));
		}
	}, [git, sessionId]);
	useEffect(() => {
		const controller = new AbortController();
		void load(controller.signal);
		return () => controller.abort();
	}, [load]);
	const refresh = async () => {
		setRefreshing(true);
		try { await load(); } finally { setRefreshing(false); }
	};

	const branches = list?.branches ?? [];
	const choose = (id: string) => {
		const row = branches.find((item) => branchId(branchOfRow(item)) === id);
		haptics.select();
		backOr(router);
		if (row && id !== selected) takeSheetResult<FilesBranch>(resultKey)?.(branchOfRow(row));
	};
	const needle = query.trim();
	return (
		<NativePickerList
			header={<>
				{/* iOS titles the sheet in its native bar. */}
				{Platform.OS === "ios" ? null : <SheetHeader title="Branch" />}
				{error ? <Text accessibilityRole="alert" style={styles.notice}>{error}</Text> : null}
				{!list && !error ? <Spinner size="large" accessibilityLabel="Loading branches" style={styles.loading} /> : null}
				{branches.length > SEARCH_FROM ? <ComposerSearchField initialQuery="" placeholder="Find a branch" onQuery={setQuery} /> : null}
				{list?.truncated ? <Text style={styles.more}>Only the newest branches are listed.</Text> : null}
			</>}
			empty={needle ? `No branches match “${needle}”.` : list ? "No branches to compare." : undefined}
			options={branchOptions(branches, query)}
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
