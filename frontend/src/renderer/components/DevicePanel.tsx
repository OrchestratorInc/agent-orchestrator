import { useCallback, useEffect, useRef, useState, type PointerEvent as ReactPointerEvent, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { Ban, ChevronLeft, CircleAlert, Ellipsis, GalleryHorizontal, House, LoaderCircle, Square } from "@/components/app-icons";
import { AndroidIcon, AppleIcon } from "./icons";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "./ui/dropdown-menu";
import type { components } from "../../api/schema";
import { apiClient, getApiBaseUrl } from "../lib/api-client";
import { apiErrorException } from "../lib/api-error-exception";
import { useWorkspaceSession } from "../hooks/useWorkspaceQuery";
import { playH264, playMjpeg } from "../lib/device-stream";
import { deviceStreamRoutes, encodeButton, encodeTouch, normalizedPoint, type DeviceButton, type TouchPhase } from "../lib/device-input";
import { cn } from "../lib/utils";
import { Button } from "./ui/button";
import { Pressable } from "./ui/pressable";
import { DeviceFrame, deviceFrameKind } from "./DeviceFrame";
import { PanelMessage, RetryButton } from "./WorkspaceDiffView";

type DeviceStatus = components["schemas"]["DeviceStatusResponse"];
type DeviceLease = components["schemas"]["DeviceLease"];
type DeviceSummary = components["schemas"]["DeviceSummary"];
type DevicePlatformStatus = components["schemas"]["DevicePlatformStatus"];

const devicesQueryKey = ["devices"] as const;
const sessionDeviceQueryKey = (sessionId: string) => ["session-device", sessionId] as const;

async function fetchDeviceStatus(): Promise<DeviceStatus> {
	const { data, error } = await apiClient.GET("/api/v1/devices");
	if (error || !data) throw apiErrorException(error, "device status unavailable");
	return data;
}

async function fetchSessionDevice(sessionId: string): Promise<DeviceLease | null> {
	const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/device", { params: { path: { sessionId } } });
	if (error || !data) throw apiErrorException(error, "device lease unavailable");
	return data.lease ?? null;
}

/** Whether device support is on, checked rarely: nothing about a session's device is worth polling while it is off. */
function useDeviceSupportOn(enabled: boolean) {
	return useQuery({
		queryKey: devicesQueryKey,
		queryFn: fetchDeviceStatus,
		enabled,
		refetchInterval: 30_000,
		select: (status) => status.enabled && status.state !== "off",
	}).data;
}

/**
 * The session's lease, so an agent's `ao device open` shows up. Polled only
 * while device support is on: every 2 s while the Device tab is open or a
 * device is held, every 10 s otherwise, and every 30 s after a failure (each
 * failure is reported, so a dead daemon must not be hammered).
 */
export function useSessionDevice(sessionId: string, enabled: boolean, watching = false) {
	const supportOn = useDeviceSupportOn(enabled);
	return useQuery({
		queryKey: sessionDeviceQueryKey(sessionId),
		queryFn: () => fetchSessionDevice(sessionId),
		enabled: enabled && supportOn === true,
		refetchInterval: (query) => (query.state.status === "error" ? 30_000 : watching || query.state.data ? 2_000 : 10_000),
	});
}

/**
 * The Device tab: set up the device tools, choose iOS or Android (or how to
 * install what one needs), pick a simulator or emulator, and watch and tap
 * the device the session holds. Agents open the same device
 * with `ao device open`; the lease poll picks it up.
 */
export function DevicePanel({ isActive, sessionId }: { isActive: boolean; sessionId: string }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const status = useQuery({
		queryKey: devicesQueryKey,
		queryFn: fetchDeviceStatus,
		enabled: isActive,
		refetchInterval: (query) => (query.state.data?.state === "installing" ? 1_000 : 5_000),
	});
	const lease = useSessionDevice(sessionId, isActive, true);
	// Where the user is in the tab: choosing a platform, its devices, or its setup guide.
	const [view, setView] = useState<DeviceView>({ kind: "choose" });
	// Nothing can run yet, so there are no tools to install: Set up walks
	// straight to the platforms and their install guides instead.
	const [exploring, setExploring] = useState(false);
	// Releasing a device goes back to that platform's list, not the chooser.
	const leasedPlatform = lease.data?.device.platform;
	useEffect(() => {
		if (leasedPlatform) setView({ kind: "devices", platform: leasedPlatform });
	}, [leasedPlatform]);
	const refresh = useCallback(() => {
		void queryClient.invalidateQueries({ queryKey: devicesQueryKey });
		void queryClient.invalidateQueries({ queryKey: sessionDeviceQueryKey(sessionId) });
	}, [queryClient, sessionId]);
	// "Check again" spins for the user's own check, not the background poll.
	const [checking, setChecking] = useState(false);
	const refetchStatus = status.refetch;
	const check = useCallback(() => {
		setChecking(true);
		void refetchStatus().finally(() => setChecking(false));
	}, [refetchStatus]);

	const setup = useMutation({
		mutationFn: async () => {
			const { error } = await apiClient.POST("/api/v1/devices/setup");
			if (error) throw apiErrorException(error);
		},
		onSettled: refresh,
	});
	const open = useMutation({
		mutationFn: async (device: DeviceSummary) => {
			const { error } = await apiClient.POST("/api/v1/sessions/{sessionId}/device/open", {
				params: { path: { sessionId } },
				body: { deviceId: device.id, platform: device.platform },
			});
			if (error) throw apiErrorException(error);
		},
		onSettled: refresh,
	});
	const release = useMutation({
		mutationFn: async (shutdown: boolean) => {
			const { error } = await apiClient.POST("/api/v1/sessions/{sessionId}/device/close", { params: { path: { sessionId } }, body: shutdown ? { shutdown: true } : {} });
			if (error) throw apiErrorException(error);
		},
		onSettled: refresh,
	});

	// A lease kept from before device support was switched off has no hub to
	// stream from: show how to turn support on rather than a device that never
	// loads. Until the status arrives, a held device shows straight away.
	const supportOff = status.data !== undefined && (!status.data.enabled || status.data.state === "off");
	if (lease.data && !supportOff) {
		const held = lease.data;
		return <DeviceLive error={release.error?.message ?? open.error?.message} isActive={isActive} lease={held} onRelease={(shutdown) => release.mutate(shutdown)} onStart={() => open.mutate(held.device)} releasing={release.isPending} sessionId={sessionId} starting={open.isPending} />;
	}
	if (status.isPending) return <PanelMessage>{t("device.loading")}</PanelMessage>;
	if (status.isError) {
		return (
			<DeviceIntro
				action={<RetryButton onClick={refresh} />}
				error={t("device.unavailable")}
			/>
		);
	}
	const current = status.data;
	const off = !current.enabled || current.state === "off";
	const canRun = current.platforms.some((platform) => platform.available);
	const platformOf = (key: DevicePlatform) => current.platforms.find((platform) => platform.platform === key);

	// Setting up a platform this machine lacks (Xcode, the Android SDK) is a
	// guide, whether or not the tools are installed yet.
	if (view.kind === "guide") {
		const platform = platformOf(view.platform);
		if (platform && !platform.available) {
			return <PlatformGuide checking={checking} onBack={() => setView({ kind: "choose" })} onCheck={check} platform={platform} />;
		}
	}
	if (current.state === "ready" && view.kind === "devices") {
		return (
			<DevicePicker
				error={open.error?.message}
				onBack={() => setView({ kind: "choose" })}
				onOpen={(device) => open.mutate(device)}
				opening={open.isPending ? open.variables?.id : undefined}
				platform={view.platform}
				sessionId={sessionId}
				status={current}
			/>
		);
	}
	// With the tools in place, or nothing able to run yet (so there is nothing to
	// install the tools for), the next step is choosing a platform.
	if (current.state === "ready" || (off && exploring)) {
		return (
			<PlatformChooser
				onChoose={(platform) => {
					if (!platform.available) return setView({ kind: "guide", platform: platform.platform });
					setView({ kind: "devices", platform: platform.platform });
					// Ready after a guide but before the tools exist: set them up now,
					// then land on this platform's devices.
					if (current.state !== "ready") {
						setExploring(false);
						setup.mutate();
					}
				}}
				platforms={current.platforms}
			/>
		);
	}
	const installing = current.state === "installing" || setup.isPending;
	const failure = setup.error?.message ?? (current.state === "error" ? (current.error ?? t("device.setupFailed")) : undefined);
	return (
		<DeviceIntro
			action={
				<Button
					disabled={installing}
					onClick={() => (off && !canRun ? setExploring(true) : setup.mutate())}
					size="sm"
					type="button"
					variant="secondary"
				>
					{installing ? <LoaderCircle aria-hidden="true" className="size-icon-sm animate-spin motion-reduce:animate-none" /> : null}
					{installing ? t("device.settingUp") : failure ? t("files.retry") : t("device.setupTools")}
				</Button>
			}
			error={failure ? shortHomePaths(failure) : undefined}
		/>
	);
}

type DevicePlatform = DevicePlatformStatus["platform"];
type DeviceView = { kind: "choose" } | { kind: "devices"; platform: DevicePlatform } | { kind: "guide"; platform: DevicePlatform };

/** Home-directory paths in daemon messages, written the way people read them: ~/Library/… */
function shortHomePaths(text: string): string {
	return text.replace(/(^|[\s"'(])\/(?:Users|home)\/[^/\s"')]+(?=\/|[\s"').,]|$)/g, "$1~");
}

/**
 * The Device tab's own page: a header in the New project panel's type, over
 * whatever this step offers, at the top of the tab like the device list. A
 * `fill` page lets its content take the rest of the tab's height.
 */
function DevicePage({ back, children, description, fill = false, title }: { back?: { label: string; onClick: () => void }; children: ReactNode; description?: ReactNode; fill?: boolean; title: ReactNode }) {
	return (
		<div className="board-scrollbar flex h-full flex-col overflow-y-auto">
			<div className={cn("flex w-full flex-col px-4 pt-4", fill ? "flex-1 pb-4" : "max-w-md pb-6")}>
				{back ? (
					<Button className="-ml-2 mb-3 self-start text-muted-foreground" onClick={back.onClick} size="sm" type="button" variant="quiet">
						<ChevronLeft aria-hidden="true" className="size-icon-sm" />
						{back.label}
					</Button>
				) : null}
				<h3 className="settings-dialog-title">{title}</h3>
				{description ? <p className="mt-1 text-[13px] leading-5 text-pretty text-muted-foreground">{description}</p> : null}
				<div className={cn("mt-5", fill && "flex flex-1 flex-col")}>{children}</div>
			</div>
		</div>
	);
}

/** Before the device tools are installed: what the tab is for, and the one way in. */
function DeviceIntro({ action, error }: { action: ReactNode; error?: string }) {
	const { t } = useTranslation();
	return (
		<DevicePage description={t("device.introBody")} title={t("device.introTitle")}>
			<div className="flex flex-col items-start gap-3">
				{error ? <p className="text-xs text-destructive [overflow-wrap:anywhere]" role="alert">{error}</p> : null}
				{action}
			</div>
		</DevicePage>
	);
}

// The daemon (backend/internal/adapters/device/local/provider.go) words the two
// reasons a platform can never run on its host this way; any other reason is
// something to install, so an unknown one reads as setup, not a dead end.
const cannotRunHere = /need macOS|does not run on/i;

function platformReady(platform: DevicePlatformStatus): "ready" | "setup" | "unavailable" {
	if (platform.available) return "ready";
	return platform.reason && cannotRunHere.test(platform.reason) ? "unavailable" : "setup";
}

const platformLook = {
	ready: { icon: null, tone: "", label: "device.statusReady" },
	setup: { icon: CircleAlert, tone: "text-status-in-review", label: "device.statusSetup" },
	unavailable: { icon: Ban, tone: "text-muted-foreground", label: "device.statusUnavailable" },
} as const;

const platformMeta = {
	ios: { icon: AppleIcon, name: "device.platformIos", hint: "device.platformIosHint" },
	android: { icon: AndroidIcon, name: "device.platformAndroid", hint: "device.platformAndroidHint" },
} as const;

/**
 * iOS and Android as two choices stacked down the tab, sharing its height,
 * each marked only when it needs setup or cannot run here.
 */
function PlatformChooser({ onChoose, platforms }: { onChoose: (platform: DevicePlatformStatus) => void; platforms: DevicePlatformStatus[] }) {
	const { t } = useTranslation();
	return (
		<DevicePage description={t("device.chooseBody")} fill title={t("device.chooseTitle")}>
			<div className="flex flex-1 flex-col gap-3">
				{platforms.map((platform) => {
					const state = platformReady(platform);
					const { icon: StateIcon, tone, label } = platformLook[state];
					const { icon: Logo, name, hint } = platformMeta[platform.platform];
					return (
						<Pressable
							aria-label={`${t(name)}, ${t(label)}`}
							className="group relative flex min-h-36 min-w-0 flex-1 flex-col items-center justify-center gap-4 rounded-lg bg-interactive-hover p-4 text-center transition-colors duration-fast hover:bg-interactive-active focus-visible:ring-2 focus-visible:ring-ring/60"
							key={platform.platform}
							onClick={() => onChoose(platform)}
						>
							{StateIcon ? <StateIcon aria-hidden="true" className={cn("absolute top-3 right-3 size-icon-base", tone)} /> : null}
							<Logo aria-hidden="true" className="size-10 text-muted-foreground transition-colors duration-fast group-hover:text-foreground" />
							<span className="min-w-0">
								<span className="block text-[15px] font-medium text-foreground">{t(name)}</span>
								<span className="mt-1 block text-pretty text-[12px] leading-5 text-muted-foreground">{state === "ready" ? t(hint) : t(label)}</span>
							</span>
						</Pressable>
					);
				})}
			</div>
		</DevicePage>
	);
}

/**
 * What to install for a platform this machine cannot run yet, as numbered
 * steps. The steps follow the daemon's reason (provider.go): no Node, no
 * Xcode, no Android SDK, or an SDK missing pieces. A reason it does not know
 * is shown as it is.
 */
function guideSteps(platform: DevicePlatformStatus, t: TFunction): { steps: string[]; note?: string } {
	const reason = platform.reason ?? "";
	const sdk = /The Android SDK at (.+?) is missing/.exec(reason)?.[1];
	const note = sdk ? t("device.guideSdkAt", { path: shortHomePaths(sdk) }) : undefined;
	if (cannotRunHere.test(reason)) return { steps: [], note: shortHomePaths(reason) };
	if (/Node\.js/.test(reason)) return { steps: [t("device.guideNode")] };
	if (platform.platform === "ios") return { steps: [t("device.guideXcodeInstall"), t("device.guideXcodeOpen"), t("device.guideXcodeRuntime")] };
	if (/command-line tools/.test(reason)) return { steps: [t("device.guideAndroidCmdline")], note };
	if (/platform-tools or the emulator/.test(reason)) return { steps: [t("device.guideAndroidEmulator")], note };
	if (/Install the Android SDK/.test(reason)) {
		return { steps: [t("device.guideAndroidStudioInstall"), t("device.guideAndroidStudioWizard"), t("device.guideAndroidCmdline"), t("device.guideAndroidAvd")] };
	}
	return { steps: [], note: shortHomePaths(reason) };
}

function PlatformGuide({ checking, onBack, onCheck, platform }: { checking: boolean; onBack: () => void; onCheck: () => void; platform: DevicePlatformStatus }) {
	const { t } = useTranslation();
	const { name } = platformMeta[platform.platform];
	const unavailable = platformReady(platform) === "unavailable";
	const { steps, note } = guideSteps(platform, t);
	return (
		<DevicePage
			back={{ label: t("device.back"), onClick: onBack }}
			description={unavailable ? undefined : t("device.guideBody")}
			title={t(unavailable ? "device.guideUnavailableTitle" : "device.guideTitle", { platform: t(name) })}
		>
			<div className="flex flex-col items-start gap-4">
				{note ? <p className="text-xs text-muted-foreground [overflow-wrap:anywhere]">{note}</p> : null}
				{steps.length > 0 ? (
					<ol className="w-full divide-y divide-border overflow-hidden rounded-md bg-settings-row text-left">
						{steps.map((step, index) => (
							<li className="flex gap-3 px-3 py-2.5" key={step}>
								<span aria-hidden="true" className="flex size-5 shrink-0 items-center justify-center rounded-full bg-interactive-active text-[11px] font-medium text-muted-foreground tabular-nums">{index + 1}</span>
								<span className="min-w-0 text-[13px] leading-5 text-foreground">{step}</span>
							</li>
						))}
					</ol>
				) : null}
				{unavailable ? null : (
					<Button disabled={checking} onClick={onCheck} size="sm" type="button" variant="secondary">
						{checking ? <LoaderCircle aria-hidden="true" className="size-icon-sm animate-spin motion-reduce:animate-none" /> : null}
						{t("device.guideCheck")}
					</Button>
				)}
			</div>
		</DevicePage>
	);
}

function DevicePicker({
	error,
	onBack,
	onOpen,
	opening,
	platform,
	sessionId,
	status,
}: {
	error?: string;
	onBack: () => void;
	onOpen: (device: DeviceSummary) => void;
	opening?: string;
	platform: DevicePlatform;
	sessionId: string;
	status: DeviceStatus;
}) {
	const { t } = useTranslation();
	const heldBy = new Map(status.leases.map((lease) => [lease.device.id, lease.sessionId]));
	const devices = status.devices
		.filter((device) => device.platform === platform)
		.sort((a, b) => Number(b.booted) - Number(a.booted) || a.name.localeCompare(b.name));
	const { icon: Logo, name } = platformMeta[platform];
	return (
		<div className="board-scrollbar flex h-full flex-col gap-3 overflow-y-auto p-3">
			<div className="flex items-center gap-1">
				<Button aria-label={t("device.back")} onClick={onBack} size="icon-sm" type="button" variant="quiet">
					<ChevronLeft aria-hidden="true" className="size-icon-sm" />
				</Button>
				<Logo aria-hidden="true" className="size-icon-md text-muted-foreground" />
				<span className="ml-1 text-sm font-medium text-foreground">{t(name)}</span>
			</div>
			<p className="text-xs text-muted-foreground">{t("device.pickHint")}</p>
			{error ? <p className="text-xs text-destructive">{error}</p> : null}
			{/* Devices that could not be listed are not an empty machine: say why. */}
			{status.error ? <p className="text-xs text-destructive [overflow-wrap:anywhere]">{shortHomePaths(status.error)}</p> : null}
			{devices.length === 0 ? (
				status.error ? null : <PanelMessage compact>{t(platform === "ios" ? "device.noIosDevices" : "device.noAndroidDevices")}</PanelMessage>
			) : (
				<ul className="shrink-0 divide-y divide-border overflow-hidden rounded-md bg-settings-row">
					{devices.map((device) => {
						const owner = heldBy.get(device.id);
						const takenByOther = owner !== undefined && owner !== sessionId;
						return (
							<li className="flex min-h-9 items-center gap-2 px-3 py-1.5" key={device.id}>
								<div className="min-w-0 flex-1">
									<p className="truncate text-sm text-foreground">{device.name}</p>
									<p className="truncate text-xs text-muted-foreground">
										{[device.version, takenByOther ? <SessionTitle key="owner" sessionId={owner} /> : device.booted ? t("device.running") : undefined]
											.filter(Boolean)
											.flatMap((part, index) => (index === 0 ? [part] : [" · ", part]))}
									</p>
								</div>
								<Button
									disabled={takenByOther || opening !== undefined}
									onClick={() => onOpen(device)}
									size="sm"
									type="button"
									variant="secondary"
								>
									{opening === device.id ? t("device.opening") : t("device.open")}
								</Button>
							</li>
						);
					})}
				</ul>
			)}
		</div>
	);
}

/** "In use by <session title>", falling back to the id while the title loads. */
function SessionTitle({ sessionId }: { sessionId: string }) {
	const { t } = useTranslation();
	const title = useWorkspaceSession(sessionId).data?.title;
	return <>{t("device.inUse", { session: title || sessionId })}</>;
}

type StreamState = "connecting" | "live" | "reconnecting";

function DeviceLive({
	error,
	isActive,
	lease,
	onRelease,
	onStart,
	releasing,
	sessionId,
	starting,
}: {
	/** A failed release, shutdown or start: said, not swallowed. */
	error?: string;
	isActive: boolean;
	lease: DeviceLease;
	onRelease: (shutdown: boolean) => void;
	onStart: () => void;
	releasing: boolean;
	sessionId: string;
	starting: boolean;
}) {
	const { t } = useTranslation();
	const { device } = lease;
	const platform = device.platform;
	const routes = deviceStreamRoutes(getApiBaseUrl(), sessionId, platform, device.id);
	const [hasFrame, setHasFrame] = useState(false);
	// The screen's shape, from the stream, so the frame around it fits.
	const [screenAspect, setScreenAspect] = useState<number>();
	const [state, setState] = useState<StreamState>("connecting");
	const socketRef = useRef<WebSocket | null>(null);
	const screenRef = useRef<HTMLCanvasElement | null>(null);
	const pointerDown = useRef(false);
	// Connections are held only while the device is on: one opened while it is
	// off goes nowhere once it boots, so turning it on opens fresh ones.
	const watching = isActive && device.booted;

	// Frames go to a canvas as each arrives: Android's H.264 through WebCodecs,
	// iOS's MJPEG newest-part-first. Screenshots only where WebCodecs is missing.
	// Nothing loads while hidden.
	useEffect(() => {
		const canvas = screenRef.current;
		if (!watching || !canvas) return;
		const shown = () => {
			setHasFrame(true);
			if (canvas.width > 0 && canvas.height > 0) setScreenAspect(canvas.width / canvas.height);
		};
		const player = routes.video ? playH264(routes.video, canvas, shown) : playMjpeg(routes.frames, canvas, shown);
		if (player) {
			return () => {
				player.stop();
				setHasFrame(false);
			};
		}
		let stopped = false;
		let timer: ReturnType<typeof setTimeout> | undefined;
		const next = () => {
			const image = new Image();
			image.crossOrigin = "anonymous";
			image.onload = image.onerror = () => {
				if (stopped) return;
				if (image.naturalWidth) {
					canvas.width = image.naturalWidth;
					canvas.height = image.naturalHeight;
					canvas.getContext("2d")?.drawImage(image, 0, 0);
					shown();
				}
				timer = setTimeout(next, 400);
			};
			image.src = `${routes.frames}${routes.frames.includes("?") ? "&" : "?"}t=${Date.now()}`;
		};
		next();
		return () => {
			stopped = true;
			if (timer) clearTimeout(timer);
			setHasFrame(false);
		};
	}, [watching, routes.frames, routes.video]);

	// Input socket, reconnecting while the tab is open and the device is on.
	useEffect(() => {
		if (!watching) return;
		let stopped = false;
		let retry: ReturnType<typeof setTimeout> | undefined;
		const connect = () => {
			const socket = new WebSocket(routes.input);
			socket.binaryType = "arraybuffer";
			socketRef.current = socket;
			socket.onopen = () => setState("live");
			socket.onclose = () => {
				if (stopped || socketRef.current !== socket) return;
				setState("reconnecting");
				retry = setTimeout(connect, 1_500);
			};
			socket.onerror = () => socket.close();
		};
		setState("connecting");
		connect();
		return () => {
			stopped = true;
			if (retry) clearTimeout(retry);
			const socket = socketRef.current;
			socketRef.current = null;
			socket?.close();
		};
	}, [watching, routes.input]);

	const send = useCallback((payload: Uint8Array<ArrayBuffer> | string | null) => {
		const socket = socketRef.current;
		if (payload !== null && socket?.readyState === WebSocket.OPEN) socket.send(payload);
	}, []);
	const touch = useCallback(
		(phase: TouchPhase, event: ReactPointerEvent<HTMLCanvasElement>) => {
			const rect = screenRef.current?.getBoundingClientRect();
			if (!rect) return;
			const point = normalizedPoint(rect, event.clientX, event.clientY);
			send(encodeTouch(platform, phase, point.x, point.y));
		},
		[platform, send],
	);
	const [pressError, setPressError] = useState<string>();
	const press = (button: DeviceButton) => {
		const viaSocket = () => send(encodeButton(platform, button));
		setPressError(undefined);
		// The simulator's Home and app switcher buttons do nothing on a Face ID
		// iPhone; the daemon makes the home indicator swipe instead. Only its
		// "send it on the socket" answer falls back there; any other failure,
		// including a request that never got an answer (the daemon may have pressed
		// it already), is said rather than pressed a second time.
		if (platform === "ios" && (button === "recents" || button === "home")) {
			void apiClient
				.POST("/api/v1/sessions/{sessionId}/device/button", { params: { path: { sessionId } }, body: { button } })
				.then(({ error }) => {
					if (!error) return;
					if (error.code === "DEVICE_BUTTON_UNSUPPORTED") viaSocket();
					else setPressError(apiErrorException(error, "The button press failed.").message);
				})
				.catch((cause: unknown) => setPressError(cause instanceof Error && cause.message ? cause.message : "The button press failed."));
			return;
		}
		viaSocket();
	};
	// The phone's own buttons, named, in its order. iOS has no Back, and its
	// app switcher is a row of cards rather than Android's square.
	const buttons: { button: DeviceButton; icon: typeof House; label: string }[] = [
		...(platform === "android" ? [{ button: "back" as const, icon: ChevronLeft, label: t("device.back") }] : []),
		{ button: "home", icon: House, label: t("device.home") },
		{ button: "recents", icon: platform === "ios" ? GalleryHorizontal : Square, label: t("device.recents") },
	];

	return (
		<div className="flex h-full min-h-0 flex-col">
			{/* The phone's layout: the device's name and OS, and one menu for what an
			    icon cannot say (giving the device back, powering it off). */}
			<div className="flex min-h-10 shrink-0 items-center gap-2 border-b border-border px-3">
				<div className="min-w-0 flex-1">
					<p className="truncate text-sm text-foreground">{device.name}</p>
					{device.version ? <p className="truncate text-xs text-muted-foreground">{device.version}</p> : null}
				</div>
				<DropdownMenu>
					<DropdownMenuTrigger asChild>
						<Button aria-label={t("device.options")} size="icon-sm" type="button" variant="ghost">
							<Ellipsis aria-hidden="true" className="size-icon-sm" />
						</Button>
					</DropdownMenuTrigger>
					<DropdownMenuContent align="end">
						<DropdownMenuItem disabled={releasing} onSelect={() => onRelease(false)}>{t("device.release")}</DropdownMenuItem>
						<DropdownMenuItem disabled={releasing} onSelect={() => onRelease(true)} variant="destructive">{t("device.shutdown")}</DropdownMenuItem>
					</DropdownMenuContent>
				</DropdownMenu>
			</div>
			{/* A size container, so the device frame can be as large as fits both ways. */}
			<div className="flex min-h-0 flex-1 items-center justify-center overflow-hidden p-4 [container-type:size]">
				{/* Shut down outside AO: say so rather than hold the last frame. */}
				{device.booted ? null : (
					<div className="flex flex-col items-center gap-2">
						<p className="text-sm text-muted-foreground">{t("device.offLive", { name: device.name })}</p>
						<Button disabled={starting} onClick={onStart} size="sm" type="button" variant="outline">{t("device.start")}</Button>
					</div>
				)}
				<DeviceFrame className={cn(!device.booted && "hidden")} kind={deviceFrameKind(platform, device.name, screenAspect)} screenAspect={screenAspect}>
					{hasFrame ? null : <p className="absolute inset-0 grid place-items-center text-xs text-muted-foreground">{t("device.connecting")}</p>}
					<canvas
						aria-label={t("device.screenAlt", { name: device.name })}
						className={cn("block size-full cursor-pointer touch-none select-none", !hasFrame && "invisible")}
						role="img"
						onPointerCancel={(event) => {
							if (!pointerDown.current) return;
							pointerDown.current = false;
							touch("end", event);
						}}
						onPointerDown={(event) => {
							pointerDown.current = true;
							event.currentTarget.setPointerCapture(event.pointerId);
							touch("begin", event);
						}}
						onPointerMove={(event) => {
							if (pointerDown.current) touch("move", event);
						}}
						onPointerUp={(event) => {
							if (!pointerDown.current) return;
							pointerDown.current = false;
							touch("end", event);
						}}
						ref={screenRef}
					/>
				</DeviceFrame>
			</div>
			<div className="flex shrink-0 flex-col items-center gap-1 px-3 pb-3">
				{state === "live" ? null : <p className="text-xs text-muted-foreground">{state === "reconnecting" ? t("device.reconnecting") : t("device.connecting")}</p>}
				{pressError || error ? <p className="text-xs text-destructive" role="alert">{pressError || error}</p> : null}
				<div className="flex items-center divide-x divide-border overflow-hidden rounded-full border border-border bg-card">
					{buttons.map(({ button, icon: Icon, label }) => (
						<Button className="h-8 gap-1.5 rounded-none px-3" disabled={state !== "live"} key={button} onClick={() => press(button)} size="sm" type="button" variant="ghost">
							<Icon aria-hidden="true" className="size-icon-sm" />
							{label}
						</Button>
					))}
				</div>
			</div>
		</div>
	);
}
