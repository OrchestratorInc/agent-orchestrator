import { useLocalSearchParams, useRouter } from "expo-router";
import { createContext, useContext, useMemo, useRef, type ReactNode } from "react";
import { ActivityIndicator, StyleSheet, View } from "react-native";
import { useCloudAuth } from "../cloud/authStore";
import { useCloudSignInAction } from "../cloud/useCloudSignInAction";
import type { SourceRef } from "../environment/scopedBoard";
import { HostScope, useApp } from "../store";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import type { Theme } from "../theme";
import { space } from "../tokens";
import { Button, EmptyState } from "../ui";
import type { FilesRouteOwner } from "./navigation";
import { cloudReviewKey, reviewKey } from "./reviewStore";
import { resolveFilesRoute, type FilesRouteParams, type FilesRouteResolution } from "./route";
import type { WorkspaceSource } from "./workspaceSource";

type FilesContextValue = {
	sessionId: string;
	source: SourceRef;
	workspace: WorkspaceSource;
	owner: FilesRouteOwner;
	reviewKey: string;
};
const FilesContext = createContext<FilesContextValue | null>(null);

export function useFilesScope(): FilesContextValue {
	const scope = useContext(FilesContext);
	if (!scope) throw new Error("Files must be opened within their owning workspace.");
	return scope;
}

/** Both Files routes resolve the same explicit source before mounting any reader. */
export function FilesScope({ children }: { children: ReactNode }) {
	const params = useLocalSearchParams<FilesRouteParams>();
	const route = resolveFilesRoute(params);
	return route.kind === "found" && route.source.kind === "local"
		? <HostScope hostId={route.source.id} key={route.source.id}><ResolvedFilesScope route={route}>{children}</ResolvedFilesScope></HostScope>
		: <ResolvedFilesScope route={route}>{children}</ResolvedFilesScope>;
}

function ResolvedFilesScope({ route, children }: { route: FilesRouteResolution; children: ReactNode }) {
	const { sourceFor, configForHost, loading } = useApp();
	const auth = useCloudAuth();
	const signIn = useCloudSignInAction();
	const router = useRouter();
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const currentSource = route.kind === "found" ? sourceFor(route.source) : undefined;
	const workspace = useMemo(() => {
		const base = currentSource?.workspace;
		if (route.kind !== "found" || route.source.kind !== "cloud") return base;
		return base?.forRepository ? base.forRepository(route.repository) : route.repository ? undefined : base;
	}, [currentSource?.workspace, route.kind === "found" ? route.repository : undefined, route.kind === "found" ? route.source.kind : undefined]);
	// A renewed account lease or paired endpoint must remount selection, detail,
	// pending requests and menus, even when the worker id itself is unchanged.
	const generation = useRef({ workspace, value: 0 });
	if (generation.current.workspace !== workspace) generation.current = { workspace, value: generation.current.value + 1 };
	const value = useMemo<FilesContextValue | null>(() => route.kind === "found" && workspace ? {
		sessionId: route.sessionId,
		source: route.source,
		workspace,
		owner: { source: route.source, scopeKey: route.source.kind === "cloud" ? route.scopeKey : undefined, repository: route.repository },
		reviewKey: route.source.kind === "cloud" ? cloudReviewKey(route.scopeKey!, route.sessionId, route.repository) : reviewKey(route.source.id, route.sessionId),
	} : null, [route.kind === "found" ? route.sessionId : undefined, route.kind === "found" ? route.source.kind : undefined, route.kind === "found" ? route.source.id : undefined, route.kind === "found" ? route.scopeKey : undefined, route.kind === "found" ? route.repository : undefined, workspace]);
	const openBoard = <Button title="Open Workers" onPress={() => router.navigate("/")} />;
	const pending = <View style={styles.center}><ActivityIndicator color={t.accent} /></View>;
	if (route.kind !== "found") return <View style={styles.center}><EmptyState icon="search" title="Files source unavailable" message="Open Files from the worker that owns them." action={openBoard} /></View>;
	if (route.source.kind === "cloud") {
		if (!currentSource) {
			if (auth.signedIn === null || auth.busy || (!auth.orgId && auth.orgLoading)) return pending;
			if (!auth.signedIn) return <View style={styles.center}><EmptyState icon="cloud" title="Sign in to Orchestrator Cloud" message="Sign in to the account that owns these files." action={<Button title="Sign in to Orchestrator Cloud" loading={signIn.busy} onPress={() => void signIn.signIn()} />} /></View>;
			if (auth.orgId && auth.orgId !== route.source.id) return <View style={styles.center}><EmptyState icon="cloud" title="Cloud workspace changed" message="Open Files from a worker in your current workspace." action={openBoard} /></View>;
			return <View style={styles.center}><EmptyState icon="wifi-off" title="Couldn't load Cloud workspace" message={auth.orgError ?? "Reconnect to Cloud to open these files."} action={<Button title="Retry" onPress={() => void auth.retryOrgResolution()} />} /></View>;
		}
		if (route.scopeKey !== currentSource.scopeKey || !workspace?.isCurrent()) return <View style={styles.center}><EmptyState icon="cloud" title="Cloud workspace changed" message="Open Files from a worker in your current account." action={openBoard} /></View>;
	} else {
		const config = configForHost(route.source.id);
		if (!config && loading) return pending;
		if (!config || config.hostId !== route.source.id || !workspace?.isCurrent()) return <View style={styles.center}><EmptyState icon="wifi-off" title="Machine offline" message="Files load once this machine reconnects." action={openBoard} /></View>;
	}
	if (!value) return <View style={styles.center}><EmptyState icon="folder" title="Files unavailable" message="This worker doesn't support browsing files." action={openBoard} /></View>;
	return <FilesContext.Provider key={JSON.stringify([route.source.kind, route.source.id, route.scopeKey, route.sessionId, route.repository, generation.current.value])} value={value}>{children}</FilesContext.Provider>;
}

const makeStyles = (t: Theme) => StyleSheet.create({
	center: { flex: 1, alignItems: "center", justifyContent: "center", gap: space.md, padding: space.xxl, backgroundColor: t.bgBase },
});
