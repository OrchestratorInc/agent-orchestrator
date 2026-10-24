import AsyncStorage from "@react-native-async-storage/async-storage";
import { createContext, useContext, useEffect, useMemo, useSyncExternalStore, type ReactNode } from "react";
import { AppState } from "react-native";
import type { CloudNotification } from "@aoagents/cloud-client";
import type { SourceRef } from "../environment/scopedBoard";
import { shouldPoll } from "../appStatePoll";
import { useCloudAuth } from "./authStore";
import { cloudAccountScopeKey } from "./deployment";
import { createCloudNotificationOwner, type CloudNotificationSnapshot } from "./notificationOwner";

export type CloudNotificationsState = CloudNotificationSnapshot & {
	available: boolean;
	source: SourceRef | null;
	scopeKey: string | null;
	/** Includes the login epoch, so an old callback cannot act after signing in again. */
	ownerKey: string | null;
	refresh(): Promise<void>;
	loadMore(): Promise<void>;
	markRead(ids: readonly string[]): Promise<void>;
	markAllRead(): Promise<void>;
	clearOne(row: CloudNotification): Promise<void>;
};
const CloudNotificationsContext = createContext<CloudNotificationsState | null>(null);
const EMPTY: CloudNotificationSnapshot = { items: [], unreadCount: 0, loading: false, refreshing: false, loadingMore: false, hasMore: false, error: null };
const subscribeEmpty = () => () => {};
const emptySnapshot = () => EMPTY;
const idle = async () => {};
const unavailable = async () => { throw new Error("Your Cloud inbox is no longer available. Refresh and try again."); };

/** One runtime shared by the inbox and every bell, independent of the selected board. */
export function CloudNotificationsProvider({ children }: { children: ReactNode }) {
	const auth = useCloudAuth();
	const available = auth.signedIn === true;
	const scopeKey = available && auth.accountId && auth.orgId && !auth.busy
		? cloudAccountScopeKey({ baseUrl: auth.baseUrl, userId: auth.accountId, orgId: auth.orgId }) : null;
	const ownerKey = scopeKey ? JSON.stringify([auth.baseUrl.replace(/\/+$/, ""), auth.accountId, auth.orgId, auth.sessionEpoch]) : null;
	const owner = useMemo(() => scopeKey && auth.orgId && auth.accountId ? createCloudNotificationOwner({
		client: auth.client, orgId: auth.orgId, userId: auth.accountId, scopeKey,
		isCurrent: auth.isCurrentSession, storage: AsyncStorage,
	}) : null, [scopeKey, auth.client, auth.orgId, auth.accountId, auth.sessionEpoch, auth.isCurrentSession]);
	const snapshot = useSyncExternalStore(owner?.subscribe ?? subscribeEmpty, owner?.getSnapshot ?? emptySnapshot, owner?.getSnapshot ?? emptySnapshot);
	useEffect(() => {
		if (!owner) return;
		if (shouldPoll(AppState.currentState)) owner.start();
		const listener = AppState.addEventListener("change", (state) => { if (shouldPoll(state)) owner.start(); else owner.stop(); });
		return () => { listener.remove(); owner.stop(); };
	}, [owner]);
	const value = useMemo<CloudNotificationsState>(() => ({
		...snapshot,
		available,
		source: scopeKey && auth.orgId ? { kind: "cloud", id: auth.orgId } : null,
		scopeKey,
		ownerKey,
		loading: owner ? snapshot.loading : available && !auth.orgError && (auth.orgLoading || auth.busy || !auth.accountId || !auth.orgId),
		error: snapshot.error ?? (available ? auth.orgError ?? auth.error : null),
		refresh: owner?.refresh ?? idle,
		loadMore: owner?.loadMore ?? idle,
		markRead: owner?.markRead ?? unavailable,
		markAllRead: owner?.markAllRead ?? unavailable,
		clearOne: owner?.clearOne ?? unavailable,
	}), [snapshot, available, scopeKey, ownerKey, auth.orgId, auth.orgLoading, auth.orgError, auth.error, auth.busy, auth.accountId, owner]);
	return <CloudNotificationsContext.Provider value={value}>{children}</CloudNotificationsContext.Provider>;
}

export function useCloudNotifications(): CloudNotificationsState {
	const value = useContext(CloudNotificationsContext);
	if (!value) throw new Error("useCloudNotifications must be used within <CloudNotificationsProvider>");
	return value;
}
