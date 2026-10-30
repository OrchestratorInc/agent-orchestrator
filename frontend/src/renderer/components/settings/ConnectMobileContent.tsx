import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { Check, Coffee, Copy, Globe, Loader2, MonitorSmartphone, QrCode, RotateCcw, TriangleAlert } from "lucide-react";
import { apiClient, apiErrorMessage } from "../../lib/api-client";
import { mobileStatusQueryKey } from "../../lib/mobile-status-key";
import { useStayAwake } from "../../hooks/useStayAwake";
import { aoBridge } from "../../lib/bridge";
import { captureRendererEvent } from "../../lib/telemetry";
import { ANDROID_PLAY_STORE_URL, IOS_APP_STORE_URL } from "./ConnectMobileGetApp";
import { reasonMessage, type SetupMode } from "./ConnectMobileSetup";
import { PAIRING_QR_VERSION, QrCanvas } from "./QrCanvas";
import { PairingQr } from "./PairingQr";
import { scramblePairingCodes } from "./qrScramble";
import { InstallCloudflared } from "./InstallCloudflared";
import { Button } from "../ui/button";
import { Switch } from "../ui/switch";
import { SettingsRow } from "./SettingsRow";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";

// The pairing panel is a fixed 240px square (size-60) so the card stays short
// and a code change can never move the layout. Codes are drawn at that size.
const QR_CODE_SIZE = 240;
const STORE_QR_SIZE = 140;

const STORE_LINKS = [
	{
		key: "ios",
		Icon: AppleIcon,
		url: IOS_APP_STORE_URL,
		labelKey: "mobile.ios",
		ariaKey: "mobile.iosStoreAria",
		testId: "ios-store-qr",
	},
	{
		key: "android",
		Icon: AndroidIcon,
		url: ANDROID_PLAY_STORE_URL,
		labelKey: "mobile.android",
		ariaKey: "mobile.androidSignupAria",
		testId: "android-play-qr",
	},
] as const;


import {
	buildPairingOffer,
	pairingCodeUrl,
	type PairingEndpoint,
} from "../../lib/pairing-payload";

export { mobileStatusQueryKey };

// One scan gives the mobile app every value required to connect. Keep the
// secure key absent for plaintext payloads so older mobile builds can decode
// the same bytes they already understand.
export function pairingPayload(host: string, port: number, password: string, secure?: boolean): string {
	return JSON.stringify(secure ? { v: 1, host, port, password, secure: true } : { v: 1, host, port, password });
}

/**
 * The value encoded into the pairing QR.
 *
 * Prefers the v2 deep link, which carries every endpoint the daemon advertises
 * so the phone can race them, and opens the app straight from the system
 * camera. The payload rides in the fragment, so the connection token never
 * reaches a server even when the link is opened in a browser.
 *
 * Falls back to the v1 payload when the daemon advertises no endpoints — an
 * older daemon, or one whose network is not up yet. Showing a dead v2 QR there
 * would be worse than the pairing that already works.
 */
export function qrValueFor(input: {
	hostId: string;
	host: string;
	platform: string;
	password: string;
	endpoints: readonly PairingEndpoint[];
}): string {
	// v2 only. The phone no longer accepts v1: those codes predate the identity
	// probe the race uses to verify a machine before sending it a credential, so
	// a daemon old enough to need one could never complete a race anyway. An
	// empty endpoint list means there is nothing to advertise yet, and the panel
	// shows the preparing state rather than a code — see qrIsReady.
	return pairingCodeUrl(
		buildPairingOffer({
			endpoints: input.endpoints,
			password: input.password,
			hostId: input.hostId,
			name: input.host,
			platform: input.platform,
		}),
		PAIRING_LINK_BASE,
	);
}

/**
 * How often to re-read mobile status, or false to stop.
 *
 * The connector takes roughly half a minute to become advertisable, and the
 * status query is otherwise fetched once on open and refetched only after a
 * mutation. Without this the modal would sit on "Preparing remote access…"
 * indefinitely while the daemon had long since finished.
 *
 * Polls only while there is a transient state to wait out, so an idle modal
 * does not keep hitting the daemon for the rest of the session.
 */
type PairingReadinessStatus = {
	enabled: boolean;
	noPublicTunnel?: boolean;
	endpoints?: readonly PairingEndpoint[];
	tunnel?: {
		supported?: boolean;
		running: boolean;
		ready: boolean;
		lastError?: string;
		[k: string]: unknown;
	};
};

function pairingIsPending(status: PairingReadinessStatus | undefined): boolean {
	if (!status?.enabled) return false;
	if (!status.endpoints || status.endpoints.length === 0) return true;
	if (status.noPublicTunnel === true) return false;
	const tunnel = status.tunnel;
	if (!tunnel || tunnel.ready || tunnel.lastError) return false;
	return tunnel.supported === true || tunnel.running;
}

export function mobileStatusRefetchInterval(status: PairingReadinessStatus | undefined): number | false {
	return pairingIsPending(status) ? MOBILE_STATUS_POLL_MS : false;
}

const MOBILE_STATUS_POLL_MS = 2_000;

/**
 * Whether the pairing QR is safe to show.
 *
 * Pairing can start as soon as any endpoint is reachable. Remote access is
 * optional and can take up to a minute to start, so it must not block a LAN
 * code. The status query continues polling while the tunnel starts and the QR
 * is refreshed with the remote endpoint once the daemon advertises it.
 *
 * A daemon that does not report endpoints at all predates the endpoint race.
 * It has no tunnel to wait for, and its QR still works, so it is shown — the
 * absence of the field is not the same as an empty list.
 */
export function qrIsReady(status: PairingReadinessStatus): boolean {
	if (!status.enabled) return false;
	// Nothing to encode: a v2 code carries the endpoint list, and there is no
	// longer a v1 form to fall back to. An absent list is as unready as an empty
	// one — it means the daemon has not told us where it can be reached.
	if (!status.endpoints || status.endpoints.length === 0) return false;
	return true;
}

/**
 * Why the code on screen has no public tunnel, or null when it has one (or
 * when there is no code to judge). Without the tunnel the code reaches this
 * machine from elsewhere only over Tailscale, if it carries a tailnet address.
 *
 * qrIsReady shows a LAN code while the tunnel is still starting, and the
 * phone races only what the code carries. A code scanned or photographed in
 * that window looks identical to a complete one, then fails away from home
 * with "is your phone on the same Wi-Fi?" — so the panel has to say so.
 * cloudflared being absent is stated separately (remoteAccessUnavailable), and
 * opting out of the tunnel is the user's own choice, so neither is reported.
 */
export function localOnlyReason(status: PairingReadinessStatus): "starting" | "failed" | null {
	if (!status.enabled || !status.endpoints || status.endpoints.length === 0) return null;
	if (status.endpoints.some((e) => e.kind === "tunnel")) return null;
	if (status.noPublicTunnel === true || status.tunnel?.supported === false) return null;
	if (pairingIsPending(status)) return "starting";
	if (status.tunnel?.lastError) return "failed";
	return null;
}

/** The app's registered scheme (app.json `expo.scheme`), not a universal link:
 * it works today, with no association files to host and no store listing
 * required. Universal links can be added later without changing the payload. */
const PAIRING_LINK_BASE = "aomobile://pair";

/** A decoy with the same payload shape as a real pairing offer, drawn at the
 * same pinned QR version, so the blurred preview looks like the code it
 * becomes. */
function placeholderQrValue(status?: { hostId?: string; host?: string; password?: string }): string {
	return scramblePairingCodes(1, {
		...(status?.hostId ? { hostIdLength: status.hostId.length } : {}),
		...(status?.host ? { nameLength: status.host.length } : {}),
		...(status?.password ? { tokenLength: status.password.length } : {}),
	})[0];
}

function AppleIcon({ className }: { className?: string }) {
	return (
		<svg aria-hidden="true" className={className} fill="currentColor" viewBox="0 0 384 512">
			<path d="M318.7 268.7c-.2-36.7 16.4-64.4 50-84.8-18.8-26.9-47.2-41.7-84.7-44.6-35.5-2.8-74.3 20.7-88.5 20.7-15 0-49.4-19.7-76.4-19.7C63.3 141.2 4 184.8 4 273.5q0 39.3 14.4 81.2c12.8 36.7 59 126.7 107.2 125.2 25.2-.6 43-17.9 75.8-17.9 31.8 0 48.3 17.9 76.4 17.9 48.6-.7 90.4-82.5 102.6-119.3-65.2-30.7-61.7-90-61.7-91.9zm-56.6-164.2c27.3-32.4 24.8-61.9 24-72.5-24.1 1.4-52 16.4-67.9 34.9-17.5 19.8-27.8 44.3-25.6 71.9 26.1 2 49.9-11.4 69.5-34.3Z" />
		</svg>
	);
}

function AndroidIcon({ className }: { className?: string }) {
	return (
		<svg aria-hidden="true" className={className} fill="currentColor" viewBox="0 0 576 512">
			<path d="M420.55 301.93a24 24 0 1 1 24-24 24 24 0 0 1-24 24m-265.1 0a24 24 0 1 1 24-24 24 24 0 0 1-24 24m273.7-144.48 47.94-83a10 10 0 1 0-17.27-10l-48.54 84.07a301.25 301.25 0 0 0-246.56 0L116.18 64.45a10 10 0 1 0-17.27 10l47.94 83C64.53 202.22 8.24 285.55 0 384h576c-8.24-98.45-64.54-181.78-146.85-226.55" />
		</svg>
	);
}

/** One store in the segmented control: platform icon and name. */
const STORE_SEGMENT_CLASS =
	"inline-flex items-center gap-1.5 px-2.5 text-settings-label transition-colors duration-(--duration-fast) hover:bg-[color-mix(in_oklch,var(--color-bg-settings-input),var(--foreground)_6%)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-(--color-settings-accent)";

/** Icon actions beside the password. */
const ICON_ACTION_CLASS =
	"inline-flex size-6 items-center justify-center rounded-sm text-settings-muted transition-colors duration-(--duration-fast) hover:text-settings-label disabled:opacity-50";

interface MobileStatus {
	enabled: boolean;
	/** User intent, independent of whether cloudflared is installed or running. */
	noPublicTunnel?: boolean;
	browserRemoteAccess?: boolean;
	/** This machine's stable identity, echoed into the pairing code so the phone
	 * can verify the endpoints it races. Optional: a daemon predating the
	 * endpoint race does not send it, and the QR falls back to the v1 payload. */
	hostId?: string;
	/** Every advertised way to reach this daemon, in preference order. Optional
	 * for the same reason as hostId. */
	endpoints?: PairingEndpoint[];
	/** Managed remote-access connector state, for showing progress while the
	 * tunnel comes up. */
	tunnel?: {
		/** False when this machine has no connector at all — cloudflared is
		 * absent. Distinct from "not started yet", which is running:false. */
		supported?: boolean;
		running: boolean;
		ready: boolean;
		hostname: string;
		location: string;
		lastError: string;
	};
	host: string;
	tailscaleHost: string;
	port: number;
	password: string;
	warning: string;
	securePairing: {
		enabled: boolean;
		available: boolean;
		active: boolean;
		host: string;
		port: number;
		reason: string;
	};
	/** macOS-only option to hold the machine awake while the bridge is on.
	 * Optional: a daemon predating it does not send the block. */
	keepAwake?: {
		supported: boolean;
		enabled: boolean;
		active: boolean;
		hasBattery: boolean;
		lidClosed?: boolean;
		lidSupported?: boolean;
		lidActive?: boolean;
		lidReason?: string;
	};
}

export async function fetchMobileStatus(): Promise<MobileStatus> {
	const { data, error } = await apiClient.GET("/api/v1/mobile/status");
	if (error || !data) throw new Error(apiErrorMessage(error));
	return data;
}

type KeepAwakeStatus = NonNullable<MobileStatus["keepAwake"]>;

/**
 * Keep awake as last seen, for the one moment nothing better is known: a cold
 * open of this page before the status has loaded (Settings prefetches it, so
 * this is rare). The row is drawn disabled in this state rather than popping
 * in, or flipping, when the status lands.
 */
const KEEP_AWAKE_STORAGE_KEY = "ao.mobile.keepAwake";

function readLastKeepAwake(): KeepAwakeStatus | null {
	try {
		const raw = window.localStorage?.getItem(KEEP_AWAKE_STORAGE_KEY);
		if (!raw) return null;
		const saved = JSON.parse(raw) as Partial<KeepAwakeStatus> | null;
		if (typeof saved?.supported !== "boolean" || typeof saved.enabled !== "boolean") return null;
		return { supported: saved.supported, enabled: saved.enabled, active: false, hasBattery: saved.hasBattery === true };
	} catch {
		return null;
	}
}

function rememberKeepAwake(keepAwake: KeepAwakeStatus | undefined) {
	try {
		if (!keepAwake) window.localStorage?.removeItem(KEEP_AWAKE_STORAGE_KEY);
		else
			window.localStorage?.setItem(
				KEEP_AWAKE_STORAGE_KEY,
				JSON.stringify({ supported: keepAwake.supported, enabled: keepAwake.enabled, hasBattery: keepAwake.hasBattery }),
			);
	} catch {
		// Storage can be unavailable; the row then waits for the status.
	}
}

export function ConnectMobileContent({ active }: { active: boolean }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const [copied, setCopied] = useState(false);
	const [optimisticEnabled, setOptimisticEnabled] = useState<boolean | null>(null);
	// The code crossfades only after the user has generated or regenerated one
	// on this visit; opening the page shows whatever exists without a transition.
	const [animateQr, setAnimateQr] = useState(false);
	const copiedTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
	// Pinned to "lan" while the connection picker below is commented out.
	// setMode is still called by the close-reset effect.
	const [mode, setMode] = useState<SetupMode>("lan");
	/*
	const modeOptions = [
		{ value: "lan", label: t("mobile.lan") },
		{ value: "tailscale", label: t("mobile.tailscale") },
	] satisfies SettingsOption<SetupMode>[];
	*/

	useEffect(() => {
		return () => {
			if (copiedTimeoutRef.current) clearTimeout(copiedTimeoutRef.current);
		};
	}, []);

	const query = useQuery({
		queryKey: mobileStatusQueryKey,
		queryFn: fetchMobileStatus,
		enabled: active,
		// Keep refreshing through every enabled-but-not-advertisable startup
		// state — see mobileStatusRefetchInterval.
		refetchInterval: (q) => mobileStatusRefetchInterval(q.state.data),
	});

	const reportedOpen = useRef(false);
	const initialEnabled = query.data?.enabled;
	useEffect(() => {
		if (!active) {
			reportedOpen.current = false;
			setMode("lan");
			setOptimisticEnabled(null);
			return;
		}
		if (initialEnabled === undefined || reportedOpen.current) return;
		reportedOpen.current = true;
		void captureRendererEvent("ao.renderer.mobile_connect_opened", { bridge_enabled: initialEnabled });
	}, [active, initialEnabled]);

	const keepAwakeStatus = query.data?.keepAwake;
	const hasStatus = query.data !== undefined;
	useEffect(() => {
		if (hasStatus) rememberKeepAwake(keepAwakeStatus);
	}, [hasStatus, keepAwakeStatus]);

	const invalidate = () => {
		void queryClient.invalidateQueries({ queryKey: mobileStatusQueryKey });
	};

	const serverEnabled = query.data?.enabled;
	useEffect(() => {
		if (optimisticEnabled !== null && serverEnabled === optimisticEnabled) {
			setOptimisticEnabled(null);
		}
	}, [optimisticEnabled, serverEnabled]);

	const enable = useMutation({
		mutationFn: async () => {
			const { data, error } = await apiClient.POST("/api/v1/mobile/enable");
			if (error) throw new Error(apiErrorMessage(error));
			return data;
		},
		onSuccess: invalidate,
		onError: () => setOptimisticEnabled(null),
	});

	const startRemoteAccess = useMutation({
		mutationFn: async () => {
			const { data, error } = await apiClient.POST("/api/v1/mobile/remote-access");
			if (error) throw new Error(apiErrorMessage(error));
			return data;
		},
		onSuccess: invalidate,
	});

	const regenerate = useMutation({
		mutationFn: async () => {
			const { data, error } = await apiClient.POST("/api/v1/mobile/regenerate");
			if (error) throw new Error(apiErrorMessage(error));
			return data;
		},
		onSuccess: invalidate,
	});

	const disable = useMutation({
		mutationFn: async () => {
			const { error } = await apiClient.POST("/api/v1/mobile/disable");
			if (error) throw new Error(apiErrorMessage(error));
		},
		// Shutdown can finish before returning an error. Refresh the actual
		// server state while retaining the mutation error for the user.
		onSettled: invalidate,
		onError: () => setOptimisticEnabled(null),
	});

	const setSecure = useMutation({
		mutationFn: async (secureEnabled: boolean) => {
			const { data, error } = await apiClient.POST("/api/v1/mobile/secure-pairing", { body: { enabled: secureEnabled } });
			if (error) throw new Error(apiErrorMessage(error));
			return data;
		},
		onSuccess: invalidate,
	});

	// Same switch as the sidebar Stay Awake button, so the two cannot disagree.
	const stayAwake = useStayAwake(active);


	const setBrowserRemoteAccess = useMutation({
		mutationFn: async (browserRemoteAccess: boolean) => {
			const { data, error } = await apiClient.POST("/api/v1/mobile/browser-control", { body: { enabled: browserRemoteAccess } });
			if (error) throw new Error(apiErrorMessage(error));
			return data;
		},
		onMutate: async (browserRemoteAccess) => {
			await queryClient.cancelQueries({ queryKey: mobileStatusQueryKey });
			const previous = queryClient.getQueryData<MobileStatus>(mobileStatusQueryKey);
			if (previous) queryClient.setQueryData<MobileStatus>(mobileStatusQueryKey, { ...previous, browserRemoteAccess });
			return { previous };
		},
		onError: (_error, _enabled, context) => {
			if (context?.previous) queryClient.setQueryData(mobileStatusQueryKey, context.previous);
		},
		onSettled: invalidate,
	});

	// TLS turns itself on wherever Tailscale exists — it is not a switch, and it
	// is deliberately not tied to the connection picker. iOS refuses cleartext
	// to a 100.x address, so a Tailscale pairing without it works on Android and
	// fails on iPhone with nothing on either side to say why. Keying this to a
	// UI mode would mean hiding that picker also silently disables TLS, which is
	// the opposite of what a hidden control should do.
	//
	// secureAttempted keeps it to one attempt per panel session: a tailnet with
	// no certificates fails every time, and retrying on each status poll would
	// hammer the daemon. secureReasonText surfaces the failure; reopening the
	// panel is the retry.
	const secureAttempted = useRef(false);
	useEffect(() => {
		if (!active) {
			secureAttempted.current = false;
			return;
		}
		// No tailnet address on this machine means nothing for TLS to serve.
		if (!query.data?.tailscaleHost) return;
		const secure = query.data?.securePairing;
		if (!secure || secure.enabled || !secure.available) return;
		if (secureAttempted.current || setSecure.isPending) return;
		secureAttempted.current = true;
		setSecure.mutate(true);
	}, [active, query.data?.tailscaleHost, query.data?.securePairing, setSecure]);

	const status = query.data;
	const enabled = optimisticEnabled ?? status?.enabled ?? false;
	const secureActive = mode === "tailscale" && (status?.securePairing?.active ?? false);
	// Tunnel-only hosts deliberately have no LAN address. Use the same daemon
	// endpoint list for both the offer and the address shown beside it.
	const activeEndpoint = status?.endpoints?.[0];
	const activeHost = activeEndpoint?.host ?? "";
	const activePort = activeEndpoint?.port ?? 0;
	const noEndpointAvailable =
		!activeEndpoint &&
		(status?.noPublicTunnel === true || status?.tunnel?.supported === false || Boolean(status?.tunnel?.lastError));
	// No connector on this machine at all: cloudflared is absent and nothing
	// installs it. Pairing still works on the local network, so this is a
	// caveat to state rather than a failure to block on.
	const remoteAccessUnavailable = status?.tunnel ? status.tunnel.supported === false : false;
	const localOnly = status ? localOnlyReason({ ...status, enabled }) : null;
	const viaTailscale = status?.endpoints?.some((e) => e.kind === "tailscale") ?? false;
	const secureBlocked = mode === "tailscale" && (status?.securePairing?.enabled ?? false) && !secureActive;

	const busy =
		enable.isPending ||
		startRemoteAccess.isPending ||
		regenerate.isPending ||
		disable.isPending ||
		setSecure.isPending ||
		setBrowserRemoteAccess.isPending ||
		stayAwake.busy;

	const clearActionErrors = () => {
		enable.reset();
		startRemoteAccess.reset();
		regenerate.reset();
		disable.reset();
		setSecure.reset();
		setBrowserRemoteAccess.reset();
	};

	const copyPassword = async () => {
		if (!status?.password) return;
		try {
			await navigator.clipboard.writeText(status.password);
			setCopied(true);
			if (copiedTimeoutRef.current) clearTimeout(copiedTimeoutRef.current);
			copiedTimeoutRef.current = setTimeout(() => setCopied(false), 1500);
		} catch {
			// Clipboard can reject (permissions / non-secure context).
		}
	};

	const reportToggle = (next: boolean, outcome: "succeeded" | "failed") => {
		void captureRendererEvent("ao.renderer.mobile_bridge_toggled", { enabled: next, outcome });
	};

	const startBridge = () => {
		if (busy || enabled) return;
		clearActionErrors();
		setAnimateQr(true);
		setOptimisticEnabled(true);
		enable.mutate(undefined, {
			onSuccess: () => reportToggle(true, "succeeded"),
			onError: () => reportToggle(true, "failed"),
		});
	};

	const actionError =
		(enable.error instanceof Error && enable.error.message) ||
		(startRemoteAccess.error instanceof Error && startRemoteAccess.error.message) ||
		(regenerate.error instanceof Error && regenerate.error.message) ||
		(disable.error instanceof Error && disable.error.message) ||
		(setSecure.error instanceof Error && setSecure.error.message) ||
		(setBrowserRemoteAccess.error instanceof Error && setBrowserRemoteAccess.error.message) ||
		stayAwake.error ||
		null;

	const turnOff = () => {
		clearActionErrors();
		setOptimisticEnabled(false);
		disable.mutate();
	};

	// Each return is one or more direct children of the settings card: the
	// pairing is a content row, keep-awake a SettingsRow, divided by the card.
	// Loading keeps the pairing layout, and Keep awake in its last-known state,
	// so the card does not jump from one short row to the full block when the
	// status lands. Settings prefetches the status, so this is a cold start only.
	if (query.isLoading) {
		const lastKeepAwake = readLastKeepAwake();
		return (
			<>
				<PairingLayout panel={<PairingPanel code={<QrPlaceholder value={placeholderQrValue()} />} />}>
					<ScanIntro />
					<p className="sr-only" role="status">
						{t("mobile.checkingStatus")}
					</p>
				</PairingLayout>
				{lastKeepAwake?.supported && <KeepAwakeRow keepAwake={lastKeepAwake} disabled />}
			</>
		);
	}
	if (query.isError) {
		return (
			<p className="settings-content-row text-xs text-error">
				{query.error instanceof Error ? query.error.message : t("mobile.loadFailed")}
			</p>
		);
	}
	if (!status) return null;

	const showRealQR =
		enabled &&
		!secureBlocked &&
		qrIsReady({ enabled, endpoints: status?.endpoints, tunnel: status?.tunnel });
	// Computed once so the rendered v2 QR and its data attribute cannot drift.
	const qrValue = showRealQR
		? qrValueFor({
				hostId: status?.hostId ?? "",
				host: activeHost,
				platform: "",
				password: status?.password ?? "",
				endpoints: status?.endpoints ?? [],
			})
		: null;
	const secureReasonText = reasonMessage(status.securePairing?.reason ?? "", t);
	const unreachable = enabled && !qrValue && noEndpointAvailable;
	const remoteAccessOn = qrValue !== null && (status.endpoints?.some((e) => e.kind === "tunnel") ?? false);

	// One object throughout: the blurred placeholder sits at the back, the real
	// code crossfades in over it (and over the previous code on a change), and
	// the overlay says what is happening while there is no code.
	const code = (
		<QrPlaceholder value={placeholderQrValue({ ...status, host: activeHost || status.host })}>
			<PairingQr value={qrValue} size={QR_CODE_SIZE} animate={animateQr} />
			{!enabled ? (
				<div className="absolute inset-0 flex items-center justify-center">
					<Button
						type="button"
						variant="footer-primary"
						className="rounded-md shadow-lg"
						onClick={startBridge}
						disabled={busy || enabled}
					>
						{t("mobile.generate")}
					</Button>
				</div>
			) : !qrValue && !unreachable ? (
				<div
					className="absolute inset-0 flex items-center justify-center"
					data-testid="mobile-pairing-preparing"
					role="status"
					aria-label={t("mobile.checkingStatus")}
				>
					<span className="flex h-(--size-settings-action-height) items-center rounded-(--radius-settings-action) bg-(--color-bg-secondary) px-3 text-settings-muted shadow-lg">
						<Loader2 className="size-4 animate-spin" aria-hidden="true" />
					</span>
				</div>
			) : null}
		</QrPlaceholder>
	);
	// Whether the code reaches this machine from elsewhere, said in a few words
	// under the code it describes: on once the code carries the tunnel, enabling
	// while the tunnel is still starting (the code refreshes itself when it is
	// up). A tunnel that failed is a warning, stated in the details column.
	const remoteAccessStarting = qrValue !== null && !remoteAccessOn && localOnly === "starting";
	const panel = (
		<PairingPanel
			code={code}
			note={
				remoteAccessOn || remoteAccessStarting ? (
					<div
						className="flex flex-col items-center text-center text-xs leading-5 text-settings-muted"
						data-testid={remoteAccessOn ? "mobile-remote-access-on" : "mobile-remote-access-starting"}
						role="status"
					>
						<p className="flex items-center gap-1.5">
							{remoteAccessOn ? (
								<Globe className="size-3.5 shrink-0" aria-hidden="true" />
							) : (
								<Loader2 className="size-3.5 shrink-0 animate-spin" aria-hidden="true" />
							)}
							{remoteAccessOn
								? t("mobile.remoteAccessOn", "Remote access on")
								: t("mobile.remoteAccessEnabling", "Enabling remote access")}
						</p>
						{/* Until the tunnel is up the code carries no public address. A
						    tailnet address still reaches this machine from elsewhere once
						    both devices are on the tailnet, so only without one is the code
						    limited to this network. */}
						{!remoteAccessOn && (
							<p className="text-caption leading-4">
								{viaTailscale
									? t("mobile.remoteAccessEnablingHintTailscale", "Until then, reachable from elsewhere only over Tailscale")
									: t("mobile.remoteAccessEnablingHint", "Until then, works on this network only")}
							</p>
						)}
					</div>
				) : null
			}
		/>
	);

	return (
		<>
			<PairingLayout panel={panel}>
				{/* No connection picker (see modeOptions above): one v2 code carries
				    every advertised endpoint — LAN, Tailscale and tunnel — and the
				    phone races them, so the mode never changed what a scan produces.
				    TLS keys off the tailnet address, not this control. */}
				<ScanIntro />

				{qrValue && (
					<dl className="mt-5 grid grid-cols-[auto_minmax(0,1fr)] items-center gap-x-6 gap-y-1 text-control leading-6">
						<dt className="text-settings-muted">{t("mobile.address")}</dt>
						<dd className="min-w-0 truncate font-mono text-settings-label" data-testid="mobile-pairing-address">
							{`${activeHost}:${activePort}`}
						</dd>
						<dt className="text-settings-muted">{t("mobile.password")}</dt>
						<dd className="flex min-w-0 items-center">
							<span className="min-w-0 truncate font-mono text-settings-label">{status.password}</span>
							<span className="ml-2 flex shrink-0 items-center">
								<Tooltip>
									<TooltipTrigger asChild>
										<button
											type="button"
											aria-label={copied ? t("mobile.passwordCopied") : t("mobile.copyPassword")}
											className={ICON_ACTION_CLASS}
											onClick={() => void copyPassword()}
										>
											{copied ? <Check className="size-3.5" aria-hidden="true" /> : <Copy className="size-3.5" aria-hidden="true" />}
										</button>
									</TooltipTrigger>
									<TooltipContent side="bottom">
										{copied ? t("mobile.passwordCopied") : t("mobile.copyPassword")}
									</TooltipContent>
								</Tooltip>
								<Tooltip>
									<TooltipTrigger asChild>
										{/* Span so the tooltip still opens while the button is disabled. */}
										<span className="inline-flex">
											<button
												type="button"
												aria-label={t("mobile.regenerate")}
												className={ICON_ACTION_CLASS}
												disabled={busy}
												onClick={() => {
													clearActionErrors();
													setAnimateQr(true);
													regenerate.mutate();
												}}
											>
												{regenerate.isPending ? (
													<Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
												) : (
													<RotateCcw className="size-3.5" aria-hidden="true" />
												)}
											</button>
										</span>
									</TooltipTrigger>
									<TooltipContent side="bottom">{t("mobile.regenerate")}</TooltipContent>
								</Tooltip>
							</span>
						</dd>
					</dl>
				)}

				{unreachable && (
					<p className="mt-5 flex items-start gap-1.5 text-control leading-5 text-warning">
						<TriangleAlert className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
						{mode === "tailscale" ? t("mobile.noTailscaleHost") : t("mobile.noPairingHost")}
					</p>
				)}

				{/* TLS on the tailnet: shown only when it failed. */}
				{status.tailscaleHost && secureReasonText && (
					<p data-testid="secure-pairing-reason" className="mt-4 text-control leading-5 text-warning">
						{secureReasonText}
					</p>
				)}

				{qrValue && localOnly === "failed" && (
					// A LAN-only code looks exactly like a full one, so say what it
					// cannot reach. (While the tunnel is still starting, the note under
					// the code says so instead.)
					<p
						className="mt-4 flex items-start gap-1.5 text-xs leading-5 text-settings-muted"
						data-testid="mobile-local-only"
						role="status"
					>
						{/* A tailnet address does reach this machine from elsewhere, once
						    both devices are on the tailnet; only the public tunnel is missing. */}
						{viaTailscale
							? t(
									"mobile.remoteAccessFailedTailscale",
									"No public tunnel — remote access couldn't start, so away from this network the code works only over Tailscale.",
								)
							: t(
									"mobile.remoteAccessFailed",
									"Works on this network only — remote access couldn't start, so this machine can't be reached from elsewhere.",
								)}
					</p>
				)}

				{remoteAccessUnavailable && (
					// Stated rather than hidden: pairing still works on this network,
					// so this is a limitation of what the code can reach, not an
					// error. Without it the QR looks entirely normal and the user
					// discovers the gap only by being away from home.
					<div className="mt-4">
						<p className="text-xs text-settings-muted" data-testid="mobile-remote-unavailable">
							{t(
								"mobile.remoteAccessUnavailable",
								"Works on this network only — cloudflared isn't installed, so this machine can't be reached from elsewhere.",
							)}
						</p>
						{/* Deliberately not enable(): that mints a fresh password, so
						    installing remote access would invalidate the phone the user
						    had already paired. This re-checks for the binary and starts
						    the connector, leaving the credential alone. */}
						<InstallCloudflared onInstalled={() => void startRemoteAccess.mutate()} />
					</div>
				)}
				{actionError && <p className="mt-3 text-xs text-error">{actionError}</p>}

				{/* Bottom of the details column, level with the code's bottom edge:
				    the off switch belongs to the connection the details describe,
				    not to the code, and here it costs no extra row. Pulled left by its
				    padding plus its 1px transparent border so the label starts on the
				    column's text edge. */}
				{enabled && (
					<div className="mt-auto pt-4">
						<Button
							type="button"
							variant="ghost"
							size="sm"
							className="-ml-[calc(--spacing(2.5)+1px)] text-settings-muted hover:text-settings-label"
							disabled={busy}
							onClick={turnOff}
						>
							{t("mobile.disable", "Turn off mobile connection")}
						</Button>
					</div>
				)}
			</PairingLayout>

			{/* macOS only — the daemon reports it unsupported elsewhere. Shown
			    whether or not the bridge is on: the choice is remembered and takes
			    effect whenever the mobile connection is. */}
			{status.keepAwake?.supported && (
				<KeepAwakeRow
					keepAwake={{ ...status.keepAwake, enabled: stayAwake.on, lidClosed: stayAwake.lidOn }}
					disabled={busy || !stayAwake.supported}
					onCheckedChange={(next) => {
						clearActionErrors();
						stayAwake.set(next, true);
					}}
					onLidChange={(next) => {
						clearActionErrors();
						stayAwake.setLid(next);
					}}
				/>
			)}
			<SettingsRow
				icon={MonitorSmartphone}
				label={t("mobile.browserRemote.label", "Allow live browser on mobile")}
				description={t("mobile.browserRemote.help", "Streams browser content and allows touch control from paired phones. This is separate from the mobile connection and is off by default.")}
				data-testid="mobile-browser-remote-access"
			>
				<Switch
					aria-label={t("mobile.browserRemote.label", "Allow live browser on mobile")}
					disabled={busy}
					checked={status.browserRemoteAccess ?? false}
					onCheckedChange={(next) => {
						clearActionErrors();
						setBrowserRemoteAccess.mutate(next);
					}}
				/>
			</SettingsRow>
		</>
	);
}

/** Keep this Mac awake, and (when the daemon reports it) keep it awake with the
 *  lid closed. Disabled while it shows a remembered state rather than the
 *  daemon's, so it cannot be toggled on a guess; the lid row appears only for a
 *  live status, since the remembered one does not carry the lid fields. */
function KeepAwakeRow({
	keepAwake,
	disabled = false,
	onCheckedChange,
	onLidChange,
}: {
	keepAwake: KeepAwakeStatus;
	disabled?: boolean;
	onCheckedChange?: (next: boolean) => void;
	onLidChange?: (next: boolean) => void;
}) {
	const { t } = useTranslation();
	const lidNeedsAttention =
		(keepAwake.enabled && keepAwake.lidClosed) ||
		keepAwake.lidReason === "recovery_failed" ||
		keepAwake.lidReason === "lease_failed" ||
		keepAwake.lidReason === "helper_upgrade_required";
	return (
		<>
			<SettingsRow
				data-testid="mobile-keep-awake"
				icon={Coffee}
				label={t("mobile.keepAwake.label")}
				description={
					<>
						{t("mobile.keepAwake.help")}
						{keepAwake.hasBattery && (!keepAwake.lidClosed || !keepAwake.lidSupported || !keepAwake.enabled) && (
							<span data-testid="mobile-keep-awake-laptop"> {t("mobile.keepAwake.laptopNote")}</span>
						)}
					</>
				}
			>
				<Switch
					aria-label={t("mobile.keepAwake.label")}
					checked={keepAwake.enabled}
					disabled={disabled}
					onCheckedChange={onCheckedChange}
				/>
			</SettingsRow>
			{onLidChange && (keepAwake.lidSupported || keepAwake.lidClosed) && (
				<SettingsRow
					data-testid="mobile-lid-awake"
					label={t("mobile.keepAwake.lidLabel", "Keep awake with lid closed")}
					description={
						<>
							{t(
								"mobile.keepAwake.lidHelp",
								"Works while mobile access is on, even when your phone disconnects. Uses more battery; keep the Mac on a ventilated surface. Stops below 10% battery or at critical heat.",
							)}
							{lidNeedsAttention && (
								<span className="mt-1 block" role="status">
									{keepAwake.lidActive && keepAwake.enabled
										? t("mobile.keepAwake.lidActive", "Closed-lid keep awake is armed.")
										: keepAwake.lidReason
											? t(
													`mobile.keepAwake.lidReason.${keepAwake.lidReason}`,
													"Closed-lid keep awake is unavailable. Turn the option off and on to retry.",
												)
											: t("mobile.keepAwake.lidIdle", "Starts when mobile access is on.")}
								</span>
							)}
						</>
					}
				>
					<Switch
						aria-label={t("mobile.keepAwake.lidLabel", "Keep awake with lid closed")}
						checked={keepAwake.enabled && (keepAwake.lidClosed ?? false)}
						disabled={disabled}
						onCheckedChange={onLidChange}
					/>
				</SettingsRow>
			)}
		</>
	);
}

/**
 * The pairing content row: details on the left, the code on the right.
 *
 * The code's offset from the card's top edge equals its offset from the right
 * edge (the row inset, 16px). The card already pads its block edges by
 * --space-1, so the row's own top padding is the inset minus that. Bottom
 * mirrors it: 16px to the divider above Keep awake, or to the card edge when
 * this row is the card's last child.
 */
function PairingLayout({ panel, children }: { panel: ReactNode; children: ReactNode }) {
	return (
		<div
			className="settings-content-row flex flex-col gap-6 pt-[calc(var(--settings-row-inset)-var(--space-1))] not-last:pb-(--settings-row-inset) last:pb-[calc(var(--settings-row-inset)-var(--space-1))] sm:flex-row"
			data-testid="mobile-pairing"
		>
			<div className="flex min-w-0 flex-1 flex-col">{children}</div>
			{panel}
		</div>
	);
}

/**
 * One instruction and where to get the app. The two stores are one compact
 * segmented control, a filled surface that stands out from the muted copy
 * around it; each segment shows its store's QR on hover or focus. Nothing here
 * changes once paired.
 */
function ScanIntro() {
	const { t } = useTranslation();
	return (
		<>
			<p className="text-sm leading-5 text-settings-label">{t("mobile.scanHint")}</p>
			<div className="mt-2 flex flex-wrap items-center gap-3 text-control leading-5">
				<span className="text-settings-muted">{t("mobile.getApp")}</span>
				<div className="inline-flex h-7 items-stretch divide-x divide-(--color-border-settings-input) overflow-hidden rounded-md bg-(--color-bg-settings-input)">
					{/* One QR glyph for the whole control: each store reveals its QR on hover. */}
					<span className="flex items-center px-2 text-settings-muted" aria-hidden="true">
						<QrCode className="size-3.5" />
					</span>
					{STORE_LINKS.map(({ key, Icon, url, labelKey, ariaKey, testId }) => (
						<Tooltip key={key}>
							<TooltipTrigger asChild>
								<button
									type="button"
									className={STORE_SEGMENT_CLASS}
									aria-label={t(ariaKey)}
									onClick={() => void aoBridge.app.openExternal(url)}
								>
									<Icon className="size-3.5 shrink-0" />
									{t(labelKey)}
								</button>
							</TooltipTrigger>
							<TooltipContent side="bottom" className="p-2" data-testid={testId}>
								<div className="rounded-md bg-(--color-bg-settings-input) p-2">
									<QrCanvas value={url} size={STORE_QR_SIZE} showLogo={false} cache className="block" />
								</div>
							</TooltipContent>
						</Tooltip>
					))}
				</div>
			</div>
		</>
	);
}

/**
 * The code column: the pairing square and an optional note beneath it. Every
 * state, loading included, renders this same element with the code first, so
 * the note coming and going never changes the code's place in the tree. A
 * different wrapper would remount the code and drop PairingQr's crossfade.
 */
function PairingPanel({ code, note }: { code: ReactNode; note?: ReactNode }) {
	return (
		<div className="flex w-60 shrink-0 flex-col gap-1.5 self-start" data-testid="mobile-pairing-panel">
			{code}
			{note}
		</div>
	);
}

/**
 * The fixed 240px square every pairing state shares: a blurred decoy code
 * (dark on white, so Android can scan the real one that replaces it) with the
 * state's content layered over it.
 */
function QrPlaceholder({ value, children }: { value: string; children?: ReactNode }) {
	return (
		<div className="relative size-60 shrink-0 self-start overflow-hidden rounded-md bg-white">
			<div className="absolute inset-0 opacity-60 blur-[6px]" aria-hidden="true">
				<QrCanvas value={value} size={QR_CODE_SIZE} version={PAIRING_QR_VERSION} cache className="block" />
			</div>
			{children}
		</div>
	);
}
