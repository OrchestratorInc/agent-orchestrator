import { useLocalSearchParams, useNavigation, useRouter } from "expo-router";
import { useCallback, useLayoutEffect } from "react";
import { StyleSheet, View } from "react-native";
import { hostRouteMatches } from "../../../lib/hostRoute";
import { Spinner } from "../../../lib/Spinner";
import { HostScope, useApp } from "../../../lib/store";
import type { Theme } from "../../../lib/theme";
import { useThemedStyles } from "../../../lib/ThemeProvider";
import { ToastHost } from "../../../lib/Toast";
import { space } from "../../../lib/tokens";
import { Button, EmptyState } from "../../../lib/ui";
import { useToast } from "../../../lib/useToast";
import { ReviewTray } from "../../../lib/files/components";
import type { FileRowAction } from "../../../lib/files/FileRowMenu.types";
import { FolderList } from "../../../lib/files/FolderList";
import { splitPath } from "../../../lib/files/model";
import { reviewKey } from "../../../lib/files/reviewStore";
import { useFileActions } from "../../../lib/files/useFileActions";
import { useReviewSender } from "../../../lib/files/useReviewSender";

export { RouteErrorBoundary as ErrorBoundary } from "../../../lib/RouteErrorBoundary";

/**
 * One folder of the session's worktree, titled with its name. Each folder is
 * its own page, so going back climbs a level and the back button's history
 * lists the way down.
 */
export default function SessionFolderScreen() {
	const { hostId } = useLocalSearchParams<{ hostId?: string }>();
	return hostId ? <HostScope key={hostId} hostId={hostId}><SessionFolderContent /></HostScope> : <SessionFolderContent />;
}

function SessionFolderContent() {
	const { sessionId, hostId: routeHostId, dir: dirParam } = useLocalSearchParams<{ sessionId: string; hostId?: string; dir?: string }>();
	const id = String(sessionId ?? "");
	// A trailing slash would name no folder.
	const dir = String(dirParam ?? "").replace(/\/+$/, "");
	const router = useRouter();
	const navigation = useNavigation();
	const { config, currentHostId, connection, loading: appLoading } = useApp();
	const hostMatches = hostRouteMatches(routeHostId, currentHostId);
	const styles = useThemedStyles(makeStyles);
	const review = useReviewSender(config, reviewKey(currentHostId, id), id);
	const toast = useToast();
	const { openFile, openFolder, act } = useFileActions({ sessionId: id, hostId: routeHostId, toast });
	const name = splitPath(dir).name || "Worktree";

	useLayoutEffect(() => {
		navigation.setOptions({ title: name });
	}, [name, navigation]);

	const openPath = useCallback((path: string, changed: boolean) => openFile(path, changed ? "diff" : "file"), [openFile]);
	const pathAction = useCallback((action: FileRowAction, path: string) => act(action, { path }), [act]);

	if (!hostMatches) return <View style={styles.center}><EmptyState icon="monitor" title="These files belong to another machine" message="Open them from that machine's session." action={<Button title="Open board" icon="workers" onPress={() => router.dismissTo("/")} />} /></View>;
	// A cold start (a deep link, a notification) lands before the saved pairing
	// loads; that is not "offline" yet.
	if (!config && appLoading) return <View style={styles.center}><Spinner size="large" accessibilityLabel="Connecting" /></View>;
	if (!config && connection === "closed") return <View style={styles.center}><EmptyState icon="wifi-off" title="Machine offline" message="Files load once the app reconnects." /></View>;

	return (
		<View style={styles.screen}>
			<FolderList
				config={config}
				sessionId={id}
				dir={dir}
				bottomInset={review.comments.length ? 96 : space.xxl}
				onOpenFile={openPath}
				onOpenFolder={openFolder}
				onAction={pathAction}
			/>
			<ReviewTray comments={review.comments} sending={review.sending} error={review.error} onSend={() => void review.send()} onRemove={review.remove} />
			<ToastHost toast={toast} />
		</View>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	center: { flex: 1, alignItems: "center", justifyContent: "center", gap: space.md, padding: space.xxl, backgroundColor: t.bgBase },
});
