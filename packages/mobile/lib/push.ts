// Client push-notification plumbing: permission, Expo token acquisition, and
// registration/unregistration with the daemon. Delivery + routing of taps lives
// in PushManager.tsx; this module owns the "get a token and tell the daemon"
// half. See docs/adr/0001-mobile-push-notifications.md (D1, D4, D7, D9).
import Constants from "expo-constants";
import * as Device from "expo-device";
import * as Notifications from "expo-notifications";
import * as SecureStore from "expo-secure-store";
import { Linking, Platform } from "react-native";
import { ApiError, announceDevice, registerPushDevice, unregisterPushDevice } from "./api";
import { configForEndpoint } from "./connect";
import { probeEndpoint } from "./connectRuntime";
import { getInstallId } from "./installId";
import type { ServerConfig } from "./config";
import { findHost, type HostMetadata } from "./hosts";
import { classifyServerFailure, hasServer, type PushRegisterResult, type PushStatus } from "./pushStatus";
import { flushMachineUnpairs, queueMachineUnpair } from "./pendingUnpair";

export type { PushRegisterResult, PushStatus } from "./pushStatus";

// Presence announcements also create daemon roster entries. Serialize them
// with unpairing so a late announcement cannot resurrect a forgotten phone.
export function announcePairedDevice(cfg: ServerConfig, device: Parameters<typeof announceDevice>[1]): Promise<void> {
	return orderedMutation(async () => {
		if (cfg.hostId) {
			const paired = await findHost(cfg.hostId);
			if (!paired || paired.token !== cfg.password) return;
		}
		await announceDevice(cfg, device);
	});
}

// Existing builds stored one registration here. Keep it for pairings without
// an identity; identified registrations are moved to a key per daemon.
const REGISTRATION_KEY = "ao.pushRegistration";
const registrationKey = (hostId: string) => `${REGISTRATION_KEY}.${hostId}`;
// Known-host registrations still owed an unregister (offline or wrong identity
// at the saved URL). Retried on the next register/foreground.
const PENDING_UNREG_KEY = "ao.pushPendingUnregister";
// Bound the pending list so a permanently-dead daemon can't grow it forever.
const MAX_PENDING_UNREG = 10;

// Finish each storage mutation before the next one reads or writes it.
let mutationTail: Promise<void> = Promise.resolve();
let refreshConnectedPush: (() => void) | undefined;
export function onManualPushRegistration(refresh: () => void): () => void {
	refreshConnectedPush = refresh;
	return () => { if (refreshConnectedPush === refresh) refreshConnectedPush = undefined; };
}

function orderedMutation<T>(action: () => Promise<T>): Promise<T> {
	const result = mutationTail.then(action, action);
	mutationTail = result.then(() => undefined, () => undefined);
	return result;
}

type Registration = {
	token: string;
	hostId?: string;
	host: string;
	httpPort: string;
	secure: boolean;
	password: string;
};

async function readRegistration(key: string): Promise<Registration | null> {
	try {
		const raw = await SecureStore.getItemAsync(key);
		return raw ? (JSON.parse(raw) as Registration) : null;
	} catch {
		return null;
	}
}

async function migrateRegistration(): Promise<void> {
	const legacy = await readRegistration(REGISTRATION_KEY);
	if (!legacy?.hostId) return;
	const key = registrationKey(legacy.hostId);
	if (!(await readRegistration(key))) {
		await SecureStore.setItemAsync(key, JSON.stringify(legacy));
	}
	await SecureStore.deleteItemAsync(REGISTRATION_KEY);
}

async function loadRegistration(cfg: ServerConfig): Promise<Registration | null> {
	await migrateRegistration();
	const reg = await readRegistration(cfg.hostId ? registrationKey(cfg.hostId) : REGISTRATION_KEY);
	return reg && sameDaemon(reg, cfg) ? reg : null;
}

async function saveRegistration(reg: Registration): Promise<void> {
	await SecureStore.setItemAsync(reg.hostId ? registrationKey(reg.hostId) : REGISTRATION_KEY, JSON.stringify(reg));
}

async function clearRegistration(reg: Registration): Promise<void> {
	await SecureStore.deleteItemAsync(reg.hostId ? registrationKey(reg.hostId) : REGISTRATION_KEY);
}

async function loadPendingUnregisters(): Promise<Registration[]> {
	try {
		const raw = await SecureStore.getItemAsync(PENDING_UNREG_KEY);
		return raw ? (JSON.parse(raw) as Registration[]) : [];
	} catch {
		return [];
	}
}

async function savePendingUnregisters(list: Registration[]): Promise<void> {
	if (list.length === 0) {
		await SecureStore.deleteItemAsync(PENDING_UNREG_KEY);
		return;
	}
	await SecureStore.setItemAsync(PENDING_UNREG_KEY, JSON.stringify(list.slice(-MAX_PENDING_UNREG)));
}

// Queue a registration for a later unregister retry (deduped by token+host).
async function queuePendingUnregister(reg: Registration): Promise<void> {
	const list = await loadPendingUnregisters();
	if (list.some((r) => r.token === reg.token && sameDaemon(r, configOf(reg)))) return;
	list.push(reg);
	await savePendingUnregisters(list);
}

// A saved URL can now point at another machine. Probe before presenting the
// saved bearer, and keep known-ID failures for a later retry.
async function unregisterIfVerified(reg: Registration): Promise<boolean> {
	if (!reg.hostId) return false;
	try {
		const answer = await probeEndpoint(
			{ kind: "lan", host: reg.host, port: Number(reg.httpPort), secure: reg.secure },
			new AbortController().signal,
		);
		if (answer.hostId !== reg.hostId) return false;
		await unregisterPushDevice(configOf(reg), reg.token);
		return true;
	} catch {
		return false;
	}
}

// Retry every queued unregister; keep only known-ID failures. A legacy entry
// has no identity to verify, so replaying it could leak its bearer to a new host.
async function flushPendingUnregisters(): Promise<void> {
	const list = await loadPendingUnregisters();
	if (list.length === 0) return;
	const stillPending: Registration[] = [];
	for (const reg of list) {
		// Old builds queued unregisters when switching machines. A registration
		// still enabled for that host supersedes the queued removal.
		if (reg.hostId && (await readRegistration(registrationKey(reg.hostId)))?.token === reg.token) continue;
		if (reg.hostId && !(await unregisterIfVerified(reg))) stillPending.push(reg);
	}
	await savePendingUnregisters(stillPending);
}

// Rebuild a minimal ServerConfig for talking to the daemon a registration names.
// muxPort is unused by the REST calls (register/unregister) so it's left empty.
function configOf(reg: Registration): ServerConfig {
	return { hostId: reg.hostId, host: reg.host, httpPort: reg.httpPort, muxPort: "", secure: reg.secure, password: reg.password };
}

// A stable id survives address changes and distinguishes hosts that reuse an
// address. Only two legacy records use the address fallback; a mixed pair
// cannot be proven to be the same machine.
function sameDaemon(reg: Registration, cfg: ServerConfig): boolean {
	if (reg.hostId && cfg.hostId) return reg.hostId === cfg.hostId;
	if (reg.hostId || cfg.hostId) return false;
	return reg.host === cfg.host && reg.httpPort === cfg.httpPort && !!reg.secure === !!cfg.secure;
}

// Suppress the OS banner while the app is foregrounded (D9) — the live in-app UI
// is the signal, so a tray banner would be a redundant double-signal. When the
// app is backgrounded/killed the OS shows the notification normally (this handler
// only runs for notifications received while the JS runtime is alive/foreground).
export function configurePushHandler(): void {
	Notifications.setNotificationHandler({
		handleNotification: async () => ({
			shouldShowBanner: false,
			shouldShowList: false,
			shouldPlaySound: false,
			shouldSetBadge: false,
		}),
	});
}

// One high-importance Android channel so `needs_input` actually buzzes (D5).
// No-op on iOS. Safe to call repeatedly.
export async function ensureAndroidChannel(): Promise<void> {
	if (Platform.OS !== "android") return;
	await Notifications.setNotificationChannelAsync("default", {
		name: "Default",
		importance: Notifications.AndroidImportance.HIGH,
	});
}

// The EAS projectId is required by getExpoPushTokenAsync. It is written into
// app.json (extra.eas.projectId) by `eas init`; fall back to the runtime
// easConfig for classic builds.
function easProjectId(): string | undefined {
	const extra = Constants.expoConfig?.extra as { eas?: { projectId?: string } } | undefined;
	return extra?.eas?.projectId ?? Constants.easConfig?.projectId;
}

// Acquire the Expo push token and register it with the daemon. Returns the token
// on success, or a typed reason on failure so the UI can say something accurate —
// notably distinguishing "this build can't mint a token" from "the server wasn't
// reachable", which are very different problems. Idempotent: the daemon upserts
// by token, so this is also the foreground-refresh path (D7).
//
// `ask` decides whether this call may spend the user's one-shot OS permission
// prompt. Automatic callers (post-connect, foreground refresh) pass false and
// register only if permission was already granted. Only a call the user
// deliberately initiated passes true: the Settings switch, and a successful QR
// scan in the pair screen. Scanning a code is the user choosing to link this
// phone to a machine, and without asking there, a new device stays unregistered
// until the user finds the Settings switch. Never pass true from the automatic
// reconnect paths: there the prompt would fire on every fresh connect, unrelated
// to anything the user just did.
async function registerForPushNow(
	cfg: ServerConfig,
	{ ask }: { ask: boolean },
): Promise<PushRegisterResult> {
	// Nothing to register with until the app is paired. Checked first, and here
	// rather than only in the UI, so no call site can spend the user's one-shot
	// permission prompt on a request that could only fail (an unpaired app still
	// holds a config object — it just has an empty host).
	if (!hasServer(cfg)) return { ok: false, reason: "not-configured" };
	// A queued refresh can outlive removal. Never resurrect its remote pairing.
	if (cfg.hostId && !(await findHost(cfg.hostId))) return { ok: false, reason: "not-configured" };
	await migrateRegistration();

	// Remote push tokens are only issued on physical devices.
	if (!Device.isDevice) return { ok: false, reason: "unsupported" };

	// Ensure the Android channel exists BEFORE the permission prompt and before
	// any notification could arrive, so a notification is never mis-filed onto an
	// implicit default channel.
	await ensureAndroidChannel();

	const current = await Notifications.getPermissionsAsync();
	let status = current.status;
	if (status !== "granted" && ask && current.canAskAgain) {
		status = (await Notifications.requestPermissionsAsync()).status;
	}
	if (status !== "granted") return { ok: false, reason: "denied" };

	const projectId = easProjectId();
	if (!projectId) {
		// Without a projectId Expo can't mint a token — this is an EAS setup gap,
		// not a runtime error. Warn and no-op so the app still works without push.
		console.warn("[push] no EAS projectId (run `eas init`); skipping push registration");
		return { ok: false, reason: "no-project-id" };
	}

	// Step 1 — mint the token. This throws when the build itself can't do push:
	// most commonly an iOS build with no APNs `aps-environment` entitlement, or a
	// simulator. Kept in its own try so it is never confused with a server error.
	let token: string;
	try {
		token = (await Notifications.getExpoPushTokenAsync({ projectId })).data;
	} catch (e) {
		console.warn("[push] could not obtain an Expo push token (build not provisioned for push?)", e);
		return { ok: false, reason: "token-failed" };
	}

	// Step 2 — hand the token to the daemon. The build is fine at this point, so
	// any failure here is about the server: either we never reached it (offline,
	// wrong host) or it answered and rejected us (bad password, lockout, 5xx).
	// An ApiError carries a status, which is exactly that distinction.
	try {
		const paired = cfg.hostId ? await findHost(cfg.hostId) : null;
		if (cfg.hostId && (!paired || paired.token !== cfg.password)) return { ok: false, reason: "not-configured" };
		const hostName = paired?.name;
		await registerPushDevice(cfg, {
			token,
			platform: Platform.OS,
			deviceName: Device.deviceName ?? undefined,
			hostName,
		});
	} catch (e) {
		const httpStatus = e instanceof ApiError ? e.status : undefined;
		console.warn(`[push] could not register the token with the daemon (status: ${httpStatus ?? "no response"})`, e);
		return { ok: false, reason: classifyServerFailure(httpStatus), status: httpStatus };
	}

	if (cfg.hostId && !(await findHost(cfg.hostId))) return { ok: false, reason: "not-configured" };
	await saveRegistration({
		token,
		hostId: cfg.hostId,
		host: cfg.host,
		httpPort: cfg.httpPort,
		secure: !!cfg.secure,
		password: cfg.password,
	});
	return { ok: true, token };
}

export function registerForPush(
	cfg: ServerConfig,
	options: { ask: boolean } = { ask: true },
): Promise<PushRegisterResult> {
	const result = orderedMutation(async () => {
		const result = await registerForPushNow(cfg, options);
		if (result.ok && options.ask) refreshConnectedPush?.();
		return result;
	});
	// Retry unregisters still owed from an earlier failure. Each probe of a dead
	// host costs up to PROBE_TIMEOUT_MS, so it runs after this call resolves
	// rather than inside it, where the Settings switch would wait on it.
	void orderedMutation(flushPendingUnregisters).catch(() => {});
	return result;
}

// Reads the live permission + registration state without prompting.
export async function getPushStatus(cfg: ServerConfig | null): Promise<PushStatus> {
	const perm = await Notifications.getPermissionsAsync();
	const reg = cfg ? await orderedMutation(() => loadRegistration(cfg)) : null;
	return {
		supported: Device.isDevice,
		granted: perm.status === "granted",
		canAskAgain: perm.canAskAgain ?? true,
		registered: !!reg && !!cfg && sameDaemon(reg, cfg),
	};
}

// Opens this app's OS settings page so the user can flip notifications back on
// after a permanent denial (the OS won't let us re-prompt in that case).
export async function openNotificationSettings(): Promise<void> {
	try {
		await Linking.openSettings();
	} catch {
		/* best-effort */
	}
}

// Capture durable cleanup before forgetting the credential. This local-only
// step deliberately does not wait behind push registration's network queue.
export async function unpairFromServer(target: HostMetadata | null): Promise<void> {
	if (!target) return;
	const host = await findHost(target.id);
	if (!host) return;
	// Pre-identity legacy pairings can still be forgotten locally, but cannot
	// safely authorize a remote retry against an address that may be reassigned.
	if (host.id) await queueMachineUnpair(host, await getInstallId());
	await migrateRegistration();
	const saved = target.id ? await readRegistration(registrationKey(target.id)) : null;
	const identified = saved?.hostId === target.id ? saved : null;
	const legacy = await readRegistration(REGISTRATION_KEY);
	// A legacy registration can only match the selected pairing by its saved
	// address. That comparison clears local state; it never authorizes a DELETE.
	const reg = identified ?? (legacy && target.endpoints.some((endpoint) => sameDaemon(legacy, configForEndpoint(endpoint, ""))) ? legacy : null);
	if (reg) await clearRegistration(reg);
}

let pendingUnpairRetry: Promise<void> | undefined;
let retryAgain = false;
let retryImmediately = false;
/** `immediate` (launch, foreground) skips the unreachable-machine backoff:
 * the network has most likely changed since the last attempt. */
export function retryPendingUnpairs(options: { immediate?: boolean } = {}): Promise<void> {
	retryImmediately ||= options.immediate === true;
	if (pendingUnpairRetry) {
		retryAgain = true;
		return pendingUnpairRetry;
	}
	pendingUnpairRetry = orderedMutation(async () => {
		do {
			retryAgain = false;
			const immediate = retryImmediately;
			retryImmediately = false;
			await flushMachineUnpairs({ ignoreBackoff: immediate });
		} while (retryAgain);
	}).finally(() => { pendingUnpairRetry = undefined; });
	return pendingUnpairRetry;
}

// Resolves once this machine's local status is cleared and the unregister it
// still owes is queued, so a suspended or killed app retries it on next launch.
// The daemon call (an identity probe plus a DELETE) drains that queue in the
// next step, so a later register still runs after it, but the caller does not
// wait on the network. A legacy registration has no identity to verify, so it
// is cleared locally and never sent.
export function unregisterFromPush(cfg: ServerConfig | null): Promise<void> {
	const cleared = orderedMutation(async () => {
		const reg = cfg ? await loadRegistration(cfg) : null;
		if (!reg) return;
		if (reg.hostId) await queuePendingUnregister(reg);
		await clearRegistration(reg);
	});
	// Queued now, not from inside the step above, so nothing queued in between
	// (a re-register) can run before this DELETE.
	void orderedMutation(flushPendingUnregisters).catch(() => {});
	return cleared;
}
