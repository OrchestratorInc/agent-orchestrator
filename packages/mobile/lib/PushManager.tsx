// Headless component mounted once under the root layout. It owns the runtime
// side of push notifications: register after pairing (+ refresh on foreground),
// and route notification taps
// (warm + cold start). See docs/adr/0001-mobile-push-notifications.md (D6, D7, D9).
import * as Device from "expo-device";
import * as Notifications from "expo-notifications";
import { useRootNavigationState, useRouter, type Href } from "expo-router";
import { useOpenPage } from "./pageNavigation";
import { useEffect, useRef } from "react";
import { Alert, AppState, Platform } from "react-native";
import { markNotificationRead } from "./api";
import { approvalPushAction } from "./approvals/approvalPush";
import { decideCloudApproval } from "./approvals/cloudApprovals";
import { decideApproval } from "./approvals/localApprovals";
import { shouldPoll } from "./appStatePoll";
import { getInstallId } from "./installId";
import { launchNotificationResponse, markNotificationResponseHandled } from "./launchNotification";
import { findHost } from "./hosts";
import { notificationTarget } from "./notificationView";
import { announcePairedDevice, configurePushHandler, ensureAndroidChannel, ensureApprovalCategory, onManualPushRegistration, registerForPush, retryPendingUnpairs } from "./push";
import { useApp } from "./store";
import { useCloud } from "./cloud/CloudProvider";
import { cloudPushTarget, foreignCloudPushOrganization } from "./cloud/pushTarget";
import { MOBILE_EVENTS } from "./telemetry/events";
import { mobileTelemetry } from "./telemetry/runtime";

/** How long a Deny from the notification waits for its machine or organization. */
const DENY_WAIT_MS = 2 * 60_000;

// Set the foreground presentation policy before any notification can arrive.
configurePushHandler();

type PushData = {
	type?: string;
	hostId?: string;
	sessionId?: string;
	projectId?: string;
	prUrl?: string;
	notificationId?: string;
	/** A browser approval's id (type "browser_approval"). */
	approvalId?: string;
	/** "cloud" for a notification Orchestrator Cloud sent rather than a paired machine. */
	source?: string;
	orgId?: string;
};

export function PushManager(): null {
	const { hostStates } = useApp();
	const router = useRouter();
	const openPage = useOpenPage();
	const navState = useRootNavigationState();

	const handledColdStart = useRef(false);
	const hostsRef = useRef(hostStates);
	hostsRef.current = hostStates;
	const cloud = useCloud();
	const cloudRef = useRef(cloud);
	cloudRef.current = cloud;
	// Denies from the notification still waiting for their machine or organization.
	const pendingDenies = useRef<Array<{ data: PushData; at: number }>>([]);
	const connected = shouldPoll(AppState.currentState)
		? hostStates.flatMap(({ config, connection }) => config && connection === "open" ? [config] : [])
		: [];
	// Polls can refresh host snapshots without changing a connection. Only a
	// changed endpoint, credential or open/closed transition refreshes push.
	const connectedKey = JSON.stringify(connected.map(({ hostId, host, httpPort, secure, password }) => [hostId, host, httpPort, secure, password, hostStates.find((state) => state.hostId === hostId)?.name]));
	const connectedRef = useRef(connected);
	connectedRef.current = connected;

	// A waiting Deny goes out once its machine connects or Cloud is ready. Past
	// the approval's own timeout it would only read "already decided": dropped.
	const openHosts = hostStates.filter(({ connection, config }) => connection === "open" && config).map(({ hostId }) => hostId).join(",");
	const cloudOrgId = cloud.org?.id;
	useEffect(() => {
		const waiting = pendingDenies.current;
		if (waiting.length === 0) return;
		pendingDenies.current = [];
		for (const entry of waiting) {
			if (Date.now() - entry.at > DENY_WAIT_MS) continue;
			void sendDeny(entry.data).then((sent) => { if (!sent) pendingDenies.current.push(entry); });
		}
		// sendDeny reads the latest hosts and organization through refs.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [openHosts, cloudOrgId]);

	// Create the Android channel once at startup.
	useEffect(() => {
		void ensureAndroidChannel();
		void ensureApprovalCategory().catch(() => {});
	}, []);

	// Runs even with zero paired hosts, including onboarding after forgetting
	// the last machine. The durable outbox also survives an app restart.
	useEffect(() => {
		const retry = (immediate: boolean) => {
			if (shouldPoll(AppState.currentState)) void retryPendingUnpairs({ immediate }).catch(() => {});
		};
		retry(true);
		const timer = setInterval(() => retry(false), 30_000);
		const sub = AppState.addEventListener("change", (state) => { if (shouldPoll(state)) retry(true); });
		return () => { clearInterval(timer); sub.remove(); };
	}, []);

	// Each daemon keeps its own device roster and push registration. Reconnect
	// re-verifies identity before a foreground refresh can use a bearer.
	useEffect(() => {
		if (connectedRef.current.length === 0) return;
		const refresh = () => {
			const configs = connectedRef.current;
			void getInstallId().then((installId) => {
				for (const config of configs) {
					void announcePairedDevice(config, {
						installId,
						platform: Platform.OS,
						deviceName: Device.deviceName ?? undefined,
					}).catch((e) => console.warn("[push] announce failed", e));
				}
			}).catch((e) => console.warn("[push] announce failed", e));
			// Automatic refresh never spends the one-shot OS permission prompt.
			for (const config of configs) {
				void registerForPush(config, { ask: false }).catch((e) => console.warn("[push] registration failed", e));
			}
		};
		refresh();
		const offManualRegistration = onManualPushRegistration(refresh);
		return offManualRegistration;
	}, [connectedKey]);

	// Route notification taps: warm via the response listener, cold start via
	// getLastNotificationResponseAsync (the listener alone misses the launch tap).
	useEffect(() => {
		if (!navState?.key) return; // wait until navigation is ready to accept routes

		const handle = (resp: Notifications.NotificationResponse | null, coldStart: boolean) => {
			if (!resp) return;
			const data = (resp.notification.request.content.data ?? {}) as PushData;
			if (approvalPushAction(data, resp.actionIdentifier)?.kind === "deny") {
				void denyFromNotification(data);
				return;
			}
			void route(data, coldStart);
		};

		// The launch's own tap only: one handled before (an earlier launch, or the
		// listener below) is marked, and RouteRestorer reads the same answer.
		if (!handledColdStart.current) {
			handledColdStart.current = true;
			void launchNotificationResponse().then((r) => handle(r, true));
		}
		const sub = Notifications.addNotificationResponseReceivedListener((r) => {
			void markNotificationResponseHandled(r);
			handle(r, false);
		});
		return () => sub.remove();
		// route() reads the latest host snapshots through hostsRef.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [navState?.key]);

	// Deny from the notification, without opening a screen. If it can't send,
	// the approval times out, which also refuses the action. On a cold start
	// (iOS wakes the app in the background for the action) no machine is
	// connected and Cloud has no organization yet, so the Deny waits for them
	// rather than being dropped.
	async function denyFromNotification(data: PushData) {
		if (approvalPushAction(data, "deny")?.kind !== "deny") return;
		if (!(await sendDeny(data))) pendingDenies.current.push({ data, at: Date.now() });
	}

	/** Sends a Deny now; false when its machine or organization isn't ready yet. */
	async function sendDeny(data: PushData): Promise<boolean> {
		const action = approvalPushAction(data, "deny");
		if (action?.kind !== "deny") return true;
		try {
			if (data.source === "cloud") {
				const { client, org } = cloudRef.current;
				if (!org) return false;
				if (action.orgId && action.orgId !== org.id) return true;
				await decideCloudApproval(client, org.id, action.sessionId, action.approvalId, "deny");
				return true;
			}
			const source = hostsRef.current.find(({ hostId }) => hostId === data.hostId);
			if (!source?.config || source.connection !== "open") return false;
			await decideApproval(source.config, action.approvalId, "deny");
		} catch (e) {
			console.warn("[push] deny from notification failed", e);
		}
		return true;
	}

	async function route(data: PushData, coldStart = false) {
		const cloudTarget = cloudPushTarget(data);
		if (cloudTarget) {
			mobileTelemetry()?.capture(MOBILE_EVENTS.notificationOpened, { target: "cloud", cold_start: coldStart });
			const { client, org, me } = cloudRef.current;
			// From an organization the phone isn't working in: its page can't load
			// here, so say where it came from rather than open one that fails.
			const elsewhere = foreignCloudPushOrganization(data, org?.id, me?.organizations ?? []);
			if (elsewhere && org) {
				Alert.alert(`From ${elsewhere}`, `The app is working in ${org.displayName}, so this notification can't open here.`);
				return;
			}
			if (org && data.notificationId && (!data.orgId || data.orgId === org.id)) void client.markNotificationRead(org.id, data.notificationId).catch(() => {});
			router.navigate(cloudTarget);
			return;
		}
		// A cold-start tap can arrive before the host runtimes are hydrated. The
		// saved pairing is the authority for whether this host may be opened.
		const paired = data.hostId ? await findHost(data.hostId).catch(() => null) : null;
		const hosts = hostsRef.current;
		const destination = notificationTarget({ type: data.type ?? "", sessionId: data.sessionId, prUrl: data.prUrl, hostId: data.hostId, approvalId: data.approvalId }, paired?.id);
		if (destination === "/") {
			// Legacy or forgotten-machine push has no safe destination here.
			router.navigate("/");
			return;
		}
		const source = hosts.find(({ hostId }) => hostId === data.hostId);
		const target = destination.startsWith("/review")
			? "review"
			: destination.startsWith("/session") ? "session" : destination.startsWith("/preview") ? "preview" : "prs";
		mobileTelemetry()?.capture(MOBILE_EVENTS.notificationOpened, { target, cold_start: coldStart });
		// Best-effort mark-read so unread counts stay consistent with the dashboard.
		if (source?.config && source.connection === "open" && data.notificationId) {
			markNotificationRead(source.config, data.notificationId).catch(() => {});
		}
		// A review opens as a page even when a sheet is up when the tap arrives.
		if (target === "review") openPage(destination as Href);
		else router.navigate(destination);
	}

	return null;
}
