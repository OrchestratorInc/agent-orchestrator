import { apiErrorException } from "../../lib/api-error-exception";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { Check, Coffee, Copy, Eye, EyeOff, Globe, Info, Loader2, MonitorSmartphone, QrCode, RotateCcw, Smartphone, TriangleAlert } from "@/components/app-icons";
import { apiClient } from "../../lib/api-client";
import { fetchPairingCode, mobilePairingCodeQueryKey, PAIRING_CODE_REFRESH_MS } from "../../lib/mobile-pairing-code";
import { mobileStatusQueryKey } from "../../lib/mobile-status-key";
import { fetchDevices, mobileDevicesQueryKey } from "./MobileDevicesSection";
import { useSettings } from "../../hooks/useSettings";
import { useStayAwake } from "../../hooks/useStayAwake";
import { useUiStore } from "../../stores/ui-store";
import { aoBridge } from "../../lib/bridge";
import { useCloudSession } from "../../lib/cloud-session";
import { AccountHostSyncError, deleteAccountRemoteHost } from "../../lib/account-remote-hosts";
import { linkThisComputer } from "../../lib/link-this-computer";
import { captureRendererEvent } from "../../lib/telemetry";
import { ANDROID_PLAY_STORE_URL, IOS_APP_STORE_URL } from "./ConnectMobileGetApp";
import { reasonMessage, type SetupMode } from "./ConnectMobileSetup";
import { PAIRING_QR_VERSION, QrCanvas } from "./QrCanvas";
import { PairingQr } from "./PairingQr";
import { scramblePairingCodes } from "./qrScramble";
import { InstallCloudflared } from "./InstallCloudflared";
import { Button } from "../ui/button";
import { Pressable } from "../ui/pressable";
import { Switch } from "../ui/switch";
import { SettingsRow } from "./SettingsRow";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";

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

export function pairingPayload(host: string, port: number, password: string, secure?: boolean): string {
	return JSON.stringify(secure ? { v: 1, host, port, password, secure: true } : { v: 1, host, port, password });
}

export function qrValueFor(input: {
	hostId: string;
	host: string;
	platform: string;
	password: string;
	endpoints: readonly PairingEndpoint[];
}): string {
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
	return tunnel.supported === true || tunnel.running || tunnel.installing === true;
}

export function mobileStatusRefetchInterval(status: PairingReadinessStatus | undefined): number | false {
	return pairingIsPending(status) ? MOBILE_STATUS_POLL_MS : false;
}

const MOBILE_STATUS_POLL_MS = 2_000;

export function qrIsReady(status: PairingReadinessStatus): boolean {
	if (!status.enabled) return false;
	if (!status.endpoints || status.endpoints.length === 0) return false;
	return true;
}

export function localOnlyReason(status: PairingReadinessStatus): "failed" | null {
	if (!status.enabled || !status.endpoints || status.endpoints.length === 0) return null;
	if (status.endpoints.some((e) => e.kind === "tunnel")) return null;
	if (status.noPublicTunnel === true || status.tunnel?.supported === false) return null;
	if (status.tunnel?.lastError) return "failed";
	return null;
}

let pairingHandledFor: unknown = null;

const PAIRING_LINK_BASE = "aomobile://pair";

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

const STORE_SEGMENT_CLASS =
	"inline-flex items-center gap-1.5 px-2.5 text-settings-label transition-colors duration-(--duration-fast) hover:bg-[color-mix(in_oklch,var(--color-bg-settings-input),var(--foreground)_6%)]";

interface MobileStatus {
	enabled: boolean;
	accountLinked?: boolean;
	noPublicTunnel?: boolean;
	browserRemoteAccess?: boolean;
	hostId?: string;
	endpoints?: PairingEndpoint[];
	tunnel?: {
		supported?: boolean;
		installing?: boolean;
		running: boolean;
		ready: boolean;
		hostname: string;
		location: string;
		lastError: string;
		fixed?: boolean;
	};
	host: string;
	tailscaleHost: string;
	port: number;
	password: string;
	requireDeviceCredentials?: boolean;
	warning: string;
	securePairing: {
		enabled: boolean;
		available: boolean;
		active: boolean;
		host: string;
		port: number;
		reason: string;
	};
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
	if (error || !data) throw apiErrorException(error);
	return data;
}

const TUNNEL_POLL_MS = 2_000;
const TUNNEL_WAIT_MS = 90_000;

export function tunnelUrl(status: MobileStatus): string | null {
	const tunnel = status.endpoints?.find((endpoint) => endpoint.kind === "tunnel" && endpoint.secure);
	return tunnel ? `https://${tunnel.host}:${tunnel.port}` : null;
}

export async function waitForTunnel(timeoutMessage: string): Promise<{ status: MobileStatus; url: string } | null> {
	let deadline = Date.now() + TUNNEL_WAIT_MS;
	for (;;) {
		const status = await fetchMobileStatus();
		const url = tunnelUrl(status);
		if (url) return { status, url };
		if (status.tunnel?.supported === false) {
			if (!status.tunnel.installing) return null;
			deadline = Date.now() + TUNNEL_WAIT_MS;
		}
		if (status.tunnel?.lastError) throw new Error(status.tunnel.lastError);
		if (Date.now() >= deadline) throw new Error(timeoutMessage);
		await new Promise((resolve) => setTimeout(resolve, TUNNEL_POLL_MS));
	}
}

type KeepAwakeStatus = NonNullable<MobileStatus["keepAwake"]>;

const KEEP_AWAKE_STORAGE_KEY = "ao.mobile.keepAwake";

function readLastKeepAwake(): KeepAwakeStatus | null {
	try {
		const raw = window.localStorage?.getItem(KEEP_AWAKE_STORAGE_KEY);
		if (!raw) return null;
		const saved = JSON.parse(raw) as Partial<KeepAwakeStatus> | null;
		if (typeof saved?.supported !== "boolean" || typeof saved.enabled !== "boolean") return null;
		return { supported: saved.supported, enabled: saved.enabled, active: false, hasBattery: saved.hasBattery === true, lidClosed: saved.lidClosed === true, lidSupported: saved.lidSupported === true };
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
				JSON.stringify({ supported: keepAwake.supported, enabled: keepAwake.enabled, hasBattery: keepAwake.hasBattery, lidClosed: keepAwake.lidClosed, lidSupported: keepAwake.lidSupported }),
			);
	} catch {
	}
}

export function ConnectMobileContent({ active }: { active: boolean }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const [copied, setCopied] = useState<"code" | "password" | null>(null);
	const [passwordShown, setPasswordShown] = useState(false);
	const [optimisticEnabled, setOptimisticEnabled] = useState<boolean | null>(null);
	const [animateQr, setAnimateQr] = useState(false);
	const copiedTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
	const [mode, setMode] = useState<SetupMode>("lan");

	useEffect(() => {
		return () => {
			if (copiedTimeoutRef.current) clearTimeout(copiedTimeoutRef.current);
		};
	}, []);

	const { session, status: sessionStatus, configured: signInConfigured } = useCloudSession();
	const { settings } = useSettings();
	const cloudBaseUrl = settings?.cloudControlPlaneUrl ?? "";
	const accountId = session?.user.id ?? "";
	const signedIn = Boolean(cloudBaseUrl && accountId);
	const [linkError, setLinkError] = useState("");
	const reportLinkError = (cause: unknown) =>
		setLinkError(cause instanceof Error && cause.message ? cause.message : t("remote.shareThisComputerFailed"));
	const linkIfSignedIn = async (
		password: string,
		hostId: string,
		options?: { ifListed?: boolean },
	): Promise<{ linked: string; added: string; failed: boolean }> => {
		setLinkError("");
		if (!signedIn || !password) return { linked: "", added: "", failed: false };
		return linkThisComputer(cloudBaseUrl, accountId, hostId, password, options).then(
			(linked) => ({ linked: linked.hostId, added: linked.added ? linked.hostId : "", failed: false }),
			(cause: unknown) => {
				if (cause instanceof AccountHostSyncError && cause.status === 409) return { linked: "", added: "", failed: false };
				reportLinkError(cause);
				return { linked: "", added: "", failed: true };
			},
		);
	};
	const linkAndStartRemoteAccess = async (password: string, hostId: string) => {
		const { added } = await linkIfSignedIn(password, hostId);
		const started = await apiClient.POST("/api/v1/mobile/remote-access");
		if (started.error) {
			if (added) await deleteAccountRemoteHost(cloudBaseUrl, added).catch(() => undefined);
			throw apiErrorException(started.error);
		}
		return started.data;
	};

	const settingsModal = useUiStore((state) => state.settingsModal);
	const startPairing = settingsModal?.scope === "global" && settingsModal.startPairing === true;

	const query = useQuery({
		queryKey: mobileStatusQueryKey,
		queryFn: fetchMobileStatus,
		enabled: active,
		refetchOnMount: startPairing ? "always" : true,
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
		void captureRendererEvent("ao.mobile.connect_opened", { bridge_enabled: initialEnabled });
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
			if (error) throw apiErrorException(error);
			if (!signedIn) return data;
			invalidate();
			return linkAndStartRemoteAccess(data?.password ?? "", data?.hostId ?? "");
		},
		onSettled: invalidate,
		onError: () => setOptimisticEnabled(null),
	});

	const startRemoteAccess = useMutation({
		mutationFn: async () => {
			if (query.data?.noPublicTunnel === true) return linkAndStartRemoteAccess(query.data.password, query.data.hostId ?? "");
			const { data, error } = await apiClient.POST("/api/v1/mobile/remote-access");
			if (error) throw apiErrorException(error);
			return data;
		},
		onSuccess: invalidate,
	});

	const regenerate = useMutation({
		mutationFn: async () => {
			const { data, error } = await apiClient.POST("/api/v1/mobile/regenerate");
			if (error) throw apiErrorException(error);
			invalidate();
			if (data && !data.noPublicTunnel) {
				const { linked, failed } = await linkIfSignedIn(data.password ?? "", data.hostId ?? "", { ifListed: true });
				if (signedIn && !linked && !failed) {
					void waitForTunnel(t("remote.shareThisComputerTimeout"))
						.then((ready) => ready && linkThisComputer(cloudBaseUrl, accountId, data.hostId ?? "", data.password ?? "", { ifListed: true, url: ready.url }))
						.catch(reportLinkError);
				}
			}
			return data;
		},
		onSuccess: invalidate,
	});

	const disable = useMutation({
		mutationFn: async () => {
			const { error } = await apiClient.POST("/api/v1/mobile/disable");
			if (error) throw apiErrorException(error);
		},
		onSettled: invalidate,
		onError: () => setOptimisticEnabled(null),
	});

	const setSecure = useMutation({
		mutationFn: async (secureEnabled: boolean) => {
			const { data, error } = await apiClient.POST("/api/v1/mobile/secure-pairing", { body: { enabled: secureEnabled } });
			if (error) throw apiErrorException(error);
			return data;
		},
		onSuccess: invalidate,
	});

	const stayAwake = useStayAwake(active);


	const setBrowserRemoteAccess = useMutation({
		mutationFn: async (browserRemoteAccess: boolean) => {
			const { data, error } = await apiClient.POST("/api/v1/mobile/browser-control", { body: { enabled: browserRemoteAccess } });
			if (error) throw apiErrorException(error);
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

	const secureAttempted = useRef(false);
	useEffect(() => {
		if (!active) {
			secureAttempted.current = false;
			return;
		}
		if (!query.data?.tailscaleHost) return;
		const secure = query.data?.securePairing;
		if (!secure || secure.enabled || !secure.available) return;
		if (secureAttempted.current || setSecure.isPending) return;
		secureAttempted.current = true;
		setSecure.mutate(true);
	}, [active, query.data?.tailscaleHost, query.data?.securePairing, setSecure]);

	const status = query.data;
	const enabled = optimisticEnabled ?? status?.enabled ?? false;
	const devices = useQuery({ queryKey: mobileDevicesQueryKey, queryFn: fetchDevices, enabled: active && status?.enabled === true });
	const pairing = useQuery({
		queryKey: mobilePairingCodeQueryKey,
		queryFn: fetchPairingCode,
		enabled: active && status?.enabled === true,
		refetchInterval: PAIRING_CODE_REFRESH_MS,
		refetchIntervalInBackground: true,
		staleTime: PAIRING_CODE_REFRESH_MS,
	});
	const [now, setNow] = useState(() => Date.now());
	useEffect(() => {
		if (!active) return;
		const timer = setInterval(() => setNow(Date.now()), 30_000);
		return () => clearInterval(timer);
	}, [active]);
	const codeExpired = pairing.data !== undefined && now >= pairing.data.expiresAt;
	useEffect(() => {
		if (codeExpired) void queryClient.invalidateQueries({ queryKey: mobilePairingCodeQueryKey });
	}, [codeExpired, queryClient]);
	const pairingCode = pairing.data && !codeExpired ? pairing.data.code : "";
	const pairingCodeFailed = pairing.isError && pairingCode === "" && qrIsReady({ enabled, endpoints: status?.endpoints, tunnel: status?.tunnel });
	const codeSpentBy = devices.data === undefined || !status ? null : `${status.password}|${devices.data.map((device) => device.id).join(",")}`;
	const lastCodeSpentBy = useRef<string | null>(null);
	useEffect(() => {
		if (codeSpentBy === null) return;
		if (lastCodeSpentBy.current !== null && lastCodeSpentBy.current !== codeSpentBy) {
			void queryClient.resetQueries({ queryKey: mobilePairingCodeQueryKey });
		}
		lastCodeSpentBy.current = codeSpentBy;
	}, [codeSpentBy, queryClient]);
	const secureActive = mode === "tailscale" && (status?.securePairing?.active ?? false);
	const activeEndpoint = status?.endpoints?.[0];
	const activeHost = activeEndpoint?.host ?? "";
	const activePort = activeEndpoint?.port ?? 0;
	const noEndpointAvailable =
		!activeEndpoint &&
		(status?.noPublicTunnel === true || status?.tunnel?.supported === false || Boolean(status?.tunnel?.lastError));
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
		setLinkError("");
	};

	const copy = async (what: "code" | "password") => {
		const text = what === "code" ? pairingCode : status?.password;
		if (!text) return;
		try {
			await navigator.clipboard.writeText(text);
			setCopied(what);
			if (copiedTimeoutRef.current) clearTimeout(copiedTimeoutRef.current);
			copiedTimeoutRef.current = setTimeout(() => setCopied(null), 1500);
		} catch {
		}
	};

	const reportToggle = (next: boolean, outcome: "succeeded" | "failed") => {
		void captureRendererEvent("ao.mobile.bridge_toggled", { enabled: next, outcome });
	};

	const turnOn = (report: boolean) => {
		if (busy || enabled) return;
		clearActionErrors();
		setAnimateQr(true);
		setOptimisticEnabled(true);
		enable.mutate(undefined, report ? {
			onSuccess: () => reportToggle(true, "succeeded"),
			onError: () => reportToggle(true, "failed"),
		} : undefined);
	};
	const startBridge = () => turnOn(true);

	useEffect(() => {
		if (!startPairing || pairingHandledFor === settingsModal) return;
		if (!active || !query.isFetchedAfterMount || query.isFetching || query.isError || busy) return;
		if (sessionStatus === "loading" || settings === undefined) return;
		pairingHandledFor = settingsModal;
		if (query.data?.enabled === false) turnOn(false);
	});

	const actionError =
		(enable.error instanceof Error && enable.error.message) ||
		(startRemoteAccess.error instanceof Error && startRemoteAccess.error.message) ||
		(regenerate.error instanceof Error && regenerate.error.message) ||
		(disable.error instanceof Error && disable.error.message) ||
		(setSecure.error instanceof Error && setSecure.error.message) ||
		(setBrowserRemoteAccess.error instanceof Error && setBrowserRemoteAccess.error.message) ||
		(pairingCodeFailed && pairing.error instanceof Error && pairing.error.message) ||
		stayAwake.error ||
		linkError ||
		null;

	const turnOff = () => {
		clearActionErrors();
		setOptimisticEnabled(false);
		disable.mutate(undefined, {
			onSuccess: () => reportToggle(false, "succeeded"),
			onError: () => reportToggle(false, "failed"),
		});
	};

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
		pairingCode !== "" &&
		qrIsReady({ enabled, endpoints: status?.endpoints, tunnel: status?.tunnel });
	const qrValue = showRealQR
		? qrValueFor({
				hostId: status?.hostId ?? "",
				host: activeHost,
				platform: "",
				password: pairingCode,
				endpoints: status?.endpoints ?? [],
			})
		: null;
	const secureReasonText = reasonMessage(status.securePairing?.reason ?? "", t);
	const unreachable = enabled && !qrValue && noEndpointAvailable;
	const remoteAccessOn = qrValue !== null && (status.endpoints?.some((e) => e.kind === "tunnel") ?? false);

	const code = (
		<QrPlaceholder value={placeholderQrValue({ ...status, host: activeHost || status.host })}>
			<PairingQr value={qrValue} size={QR_CODE_SIZE} animate={animateQr} />
			{!enabled ? (
				<div className="absolute inset-0 flex items-center justify-center">
					<Button
						type="button"
						variant="primary"
						onClick={startBridge}
						disabled={busy || enabled}
					>
						{t("mobile.generate")}
					</Button>
				</div>
			) : pairingCodeFailed ? (
				<div className="absolute inset-0 flex items-center justify-center">
					<Button type="button" variant="primary" data-testid="mobile-pairing-code-retry" onClick={() => void pairing.refetch()}>
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
	const panel = (
		<PairingPanel
			code={code}
			note={
				remoteAccessOn ? (
					<div
						className="flex flex-col items-center text-center text-xs leading-5 text-settings-muted"
						data-testid="mobile-remote-access-on"
						role="status"
					>
						<p className="flex items-center gap-1.5">
							<Globe className="size-3.5 shrink-0" aria-hidden="true" />
							{t("mobile.remoteAccessOn", "Remote access on")}
						</p>
					</div>
				) : null
			}
		/>
	);

	return (
		<>
			<PairingLayout panel={panel}>
				<ScanIntro />

				{qrValue && (
					<dl className="mt-5 grid grid-cols-[auto_minmax(0,1fr)] items-center gap-x-6 gap-y-1 text-control leading-6">
						<dt className="text-settings-muted">{t("mobile.address")}</dt>
						<dd className="min-w-0 truncate font-mono text-settings-label" data-testid="mobile-pairing-address">
							{`${activeHost}:${activePort}`}
						</dd>
						<dt className="text-settings-muted">{t("mobile.pairingCode")}</dt>
						<dd className="flex min-w-0 items-center">
							<span className="min-w-0 truncate font-mono text-settings-label" data-testid="mobile-pairing-code">{pairingCode}</span>
							<span className="ml-2 flex shrink-0 items-center">
								<Button
									type="button"
									variant="quiet"
									size="icon-xs"
									aria-label={copied === "code" ? t("mobile.pairingCodeCopied") : t("mobile.copyPairingCode")}
									onClick={() => void copy("code")}
								>
									{copied === "code" ? <Check className="size-3.5" aria-hidden="true" /> : <Copy className="size-3.5" aria-hidden="true" />}
								</Button>
								<Tooltip>
									<TooltipTrigger asChild>
										<span className="inline-flex">
											<Button
												type="button"
												variant="quiet"
												size="icon-xs"
												aria-label={t("mobile.regenerate")}
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
											</Button>
										</span>
									</TooltipTrigger>
									<TooltipContent side="bottom">{t("mobile.regenerate")}</TooltipContent>
								</Tooltip>
							</span>
						</dd>
						{!status.requireDeviceCredentials && (
							<>
								<dt className="text-settings-muted">{t("mobile.password")}</dt>
								<dd className="flex min-w-0 items-center">
									<span className="min-w-0 truncate font-mono text-settings-label" data-testid="mobile-pairing-password">
										{passwordShown ? status.password : "•".repeat(status.password.length)}
									</span>
									<span className="ml-2 flex shrink-0 items-center">
										<Button
											type="button"
											variant="quiet"
											size="icon-xs"
											aria-label={passwordShown ? t("mobile.hidePassword") : t("mobile.showPassword")}
											aria-pressed={passwordShown}
											onClick={() => setPasswordShown((shown) => !shown)}
										>
											{passwordShown ? <EyeOff className="size-3.5" aria-hidden="true" /> : <Eye className="size-3.5" aria-hidden="true" />}
										</Button>
										<Button
											type="button"
											variant="quiet"
											size="icon-xs"
											aria-label={copied === "password" ? t("mobile.passwordCopied") : t("mobile.copyPassword")}
											onClick={() => void copy("password")}
										>
											{copied === "password" ? <Check className="size-3.5" aria-hidden="true" /> : <Copy className="size-3.5" aria-hidden="true" />}
										</Button>
									</span>
								</dd>
							</>
						)}
					</dl>
				)}

				{unreachable && (
					<p className="mt-5 flex items-start gap-1.5 text-control leading-5 text-warning">
						<TriangleAlert className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
						{mode === "tailscale" ? t("mobile.noTailscaleHost") : t("mobile.noPairingHost")}
					</p>
				)}

				{status.tailscaleHost && secureReasonText && (
					<p data-testid="secure-pairing-reason" className="mt-4 text-control leading-5 text-warning">
						{secureReasonText}
					</p>
				)}

				{qrValue && localOnly === "failed" && (
					<p
						className="mt-4 flex items-start gap-1.5 text-xs leading-5 text-settings-muted"
						data-testid="mobile-local-only"
						role="status"
					>
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

				{qrValue && signInConfigured && !signedIn && !status?.tunnel?.fixed && (
					<p className="mt-4 text-xs leading-5 text-settings-muted" data-testid="mobile-sign-in-hint" role="status">
						{t("mobile.signInForFixedAddress", "Sign in to Orchestrator to give this computer an address that stays the same after restarts.")}
					</p>
				)}

				{remoteAccessUnavailable && (
					<div className="mt-4">
						{!status.tunnel?.installing && (
							<p className="text-xs text-settings-muted" data-testid="mobile-remote-unavailable">
								{t(
									"mobile.remoteAccessUnavailable",
									"Works on this network only — cloudflared isn't installed, so this machine can't be reached from elsewhere.",
								)}
							</p>
						)}
						<InstallCloudflared onInstalled={() => void startRemoteAccess.mutate()} />
					</div>
				)}
				{actionError && <p className="mt-3 text-xs text-error">{actionError}</p>}

			</PairingLayout>

			<SettingsRow icon={Smartphone} label={t("settings.connectMobile", "Connect mobile")} data-testid="mobile-connection">
				<Switch
					aria-label={t("settings.connectMobile", "Connect mobile")}
					disabled={busy}
					checked={enabled}
					onCheckedChange={(next) => (next ? startBridge() : turnOff())}
				/>
			</SettingsRow>

			{status.keepAwake?.supported && (
				<KeepAwakeRow
					keepAwake={{ ...status.keepAwake, enabled: stayAwake.on, lidClosed: stayAwake.lidOn }}
					disabled={busy || !stayAwake.supported}
					confirming={stayAwake.busy}
					onCheckedChange={(next) => {
						clearActionErrors();
						if (next) stayAwake.setLid(true);
						else stayAwake.set(false, true);
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

function KeepAwakeRow({
	keepAwake,
	disabled = false,
	confirming = false,
	onCheckedChange,
}: {
	keepAwake: KeepAwakeStatus;
	disabled?: boolean;
	confirming?: boolean;
	onCheckedChange?: (next: boolean) => void;
}) {
	const { t } = useTranslation();
	const lidUnavailable = !keepAwake.lidSupported && !keepAwake.lidClosed;
	const lidStarting = !keepAwake.lidActive && !lidUnavailable && (!keepAwake.lidReason || keepAwake.lidReason === "starting");
	const lidNeedsAttention =
		lidUnavailable ||
		(keepAwake.enabled && keepAwake.lidClosed && !(confirming && lidStarting)) ||
		keepAwake.lidReason === "recovery_failed" ||
		keepAwake.lidReason === "lease_failed" ||
		keepAwake.lidReason === "helper_upgrade_required";
	const status = keepAwake.lidActive && keepAwake.enabled
		? t("mobile.keepAwake.lidActive", "Closed-lid keep awake is armed.")
		: keepAwake.lidReason || lidUnavailable
			? t(`mobile.keepAwake.lidReason.${keepAwake.lidReason || "unsupported"}`, "Closed-lid keep awake is unavailable. Update or restart the Orchestrator desktop app to retry.")
			: t("mobile.keepAwake.lidIdle", "Closed-lid wake is starting.");
	return (
		<SettingsRow
			data-testid="mobile-keep-awake"
			icon={Coffee}
			label={t("mobile.keepAwake.label")}
			description={
				<>
					{t("mobile.keepAwake.combinedHelp")}
					<Tooltip>
						<TooltipTrigger asChild>
							<Pressable
								aria-label={t("mobile.keepAwake.details")}
								className="ml-1 inline-flex align-[-0.125em] text-settings-muted hover:text-foreground"
							>
								<Info className="size-3.5" aria-hidden="true" />
							</Pressable>
						</TooltipTrigger>
						<TooltipContent className="max-w-72">
							{t("mobile.keepAwake.lidHelp")}
							{keepAwake.lidReason === "isolated_data_dir" && <p className="mt-1">{t("mobile.keepAwake.lidReason.isolated_data_dir")}</p>}
						</TooltipContent>
					</Tooltip>
					{lidNeedsAttention && keepAwake.lidReason !== "isolated_data_dir" && (
						<span className="mt-1 block" role="status">{status}</span>
					)}
				</>
			}
		>
			<Switch
				aria-label={t("mobile.keepAwake.label")}
				checked={keepAwake.enabled && keepAwake.lidClosed === true}
				disabled={disabled || lidUnavailable}
				onCheckedChange={onCheckedChange}
			/>
		</SettingsRow>
	);
}

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

function ScanIntro() {
	const { t } = useTranslation();
	return (
		<>
			<p className="text-sm leading-5 text-settings-label">{t("mobile.scanHint")}</p>
			<div className="mt-2 flex flex-wrap items-center gap-3 text-control leading-5">
				<span className="text-settings-muted">{t("mobile.getApp")}</span>
				<div className="inline-flex h-7 items-stretch divide-x divide-(--color-border-settings-input) overflow-hidden rounded-md bg-(--color-bg-settings-input)">
					<span className="flex items-center px-2 text-settings-muted" aria-hidden="true">
						<QrCode className="size-3.5" />
					</span>
					{STORE_LINKS.map(({ key, Icon, url, labelKey, ariaKey, testId }) => (
						<Tooltip key={key}>
							<TooltipTrigger asChild>
								<Pressable
									className={STORE_SEGMENT_CLASS}
									aria-label={t(ariaKey)}
									onClick={() => void aoBridge.app.openExternal(url)}
								>
									<Icon className="size-3.5 shrink-0" />
									{t(labelKey)}
								</Pressable>
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

function PairingPanel({ code, note }: { code: ReactNode; note?: ReactNode }) {
	return (
		<div className="flex w-60 shrink-0 flex-col gap-1.5 self-start" data-testid="mobile-pairing-panel">
			{code}
			{note}
		</div>
	);
}

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
