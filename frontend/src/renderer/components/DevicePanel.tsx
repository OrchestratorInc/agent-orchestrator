import { useCallback, useEffect, useRef, useState, type PointerEvent as ReactPointerEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { ChevronLeft, Ellipsis, GalleryHorizontal, House, Square } from "@/components/app-icons";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "./ui/dropdown-menu";
import type { components } from "../../api/schema";
import { apiClient, getApiBaseUrl } from "../lib/api-client";
import { apiErrorException } from "../lib/api-error-exception";
import { useWorkspaceSession } from "../hooks/useWorkspaceQuery";
import { playH264, playMjpeg } from "../lib/device-stream";
import { deviceStreamRoutes, encodeButton, encodeTouch, normalizedPoint, type DeviceButton, type TouchPhase } from "../lib/device-input";
import { cn } from "../lib/utils";
import { Button } from "./ui/button";
import { PanelMessage, RetryButton } from "./WorkspaceDiffView";

type DeviceStatus = components["schemas"]["DeviceStatusResponse"];
type DeviceLease = components["schemas"]["DeviceLease"];
type DeviceSummary = components["schemas"]["DeviceSummary"];

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
 * The Device tab: turn device support on, pick a simulator or emulator, and
 * watch and tap the device the session holds. Agents open the same device
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
	const refresh = useCallback(() => {
		void queryClient.invalidateQueries({ queryKey: devicesQueryKey });
		void queryClient.invalidateQueries({ queryKey: sessionDeviceQueryKey(sessionId) });
	}, [queryClient, sessionId]);

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
	if (status.isError) return <PanelMessage action={<RetryButton onClick={refresh} />}>{t("device.unavailable")}</PanelMessage>;
	const current = status.data;
	if (!current.enabled || current.state === "off") {
		// Nothing to turn on without Xcode or the Android SDK: say what to install.
		const canRun = current.platforms.some((platform) => platform.available);
		return (
			<div className="flex h-full flex-col items-center justify-center gap-3 p-6 text-center">
				<h3 className="text-heading-sm font-semibold text-foreground">{t("device.offTitle")}</h3>
				<p className="max-w-xs text-sm text-muted-foreground">{t(canRun ? "device.offBody" : "device.nothingRuns")}</p>
				{canRun ? (
					<Button disabled={setup.isPending} onClick={() => setup.mutate()} type="button">
						{t("device.turnOn")}
					</Button>
				) : null}
				{setup.error ? <p className="max-w-xs text-xs text-destructive">{setup.error.message}</p> : null}
				<PlatformAvailability status={current} />
			</div>
		);
	}
	if (current.state === "installing") return <PanelMessage>{t("device.installing")}</PanelMessage>;
	if (current.state === "error") {
		return (
			<PanelMessage action={<RetryButton onClick={() => setup.mutate()} />}>
				{t("device.setupFailed")}
				{current.error ? <span className="mt-1 block text-caption">{current.error}</span> : null}
			</PanelMessage>
		);
	}
	return (
		<DevicePicker
			error={open.error?.message}
			onOpen={(device) => open.mutate(device)}
			opening={open.isPending ? open.variables?.id : undefined}
			sessionId={sessionId}
			status={current}
		/>
	);
}

function PlatformAvailability({ status }: { status: DeviceStatus }) {
	const { t } = useTranslation();
	const unavailable = status.platforms.filter((platform) => !platform.available);
	if (unavailable.length === 0) return null;
	return (
		<ul className="flex max-w-xs flex-col gap-1 text-left text-xs text-muted-foreground">
			{unavailable.map((platform) => (
				<li key={platform.platform}>
					<span className="text-foreground">{t(platform.platform === "ios" ? "device.platformIos" : "device.platformAndroid")}</span> · {platform.reason}
				</li>
			))}
		</ul>
	);
}

function DevicePicker({
	error,
	onOpen,
	opening,
	sessionId,
	status,
}: {
	error?: string;
	onOpen: (device: DeviceSummary) => void;
	opening?: string;
	sessionId: string;
	status: DeviceStatus;
}) {
	const { t } = useTranslation();
	const heldBy = new Map(status.leases.map((lease) => [lease.device.id, lease.sessionId]));
	const devices = [...status.devices].sort((a, b) => Number(b.booted) - Number(a.booted) || a.platform.localeCompare(b.platform) || a.name.localeCompare(b.name));
	return (
		<div className="board-scrollbar flex h-full flex-col gap-3 overflow-y-auto p-3">
			<p className="text-xs text-muted-foreground">{t("device.pickHint")}</p>
			{error ? <p className="text-xs text-destructive">{error}</p> : null}
			{/* Devices that could not be listed are not an empty machine: say why. */}
			{status.error ? <p className="text-xs text-destructive">{status.error}</p> : null}
			{devices.length === 0 ? (
				status.error ? null : <PanelMessage compact>{t("device.noDevices")}</PanelMessage>
			) : (
				<ul className="divide-y divide-border overflow-hidden rounded-md bg-settings-row">
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
			<PlatformAvailability status={status} />
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
		const shown = () => setHasFrame(true);
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
		// "send it on the socket" answer falls back there; any other failure is
		// said, not swallowed.
		if (platform === "ios" && (button === "recents" || button === "home")) {
			void apiClient
				.POST("/api/v1/sessions/{sessionId}/device/button", { params: { path: { sessionId } }, body: { button } })
				.then(({ error }) => {
					if (!error) return;
					if (error.code === "DEVICE_BUTTON_UNSUPPORTED") viaSocket();
					else setPressError(apiErrorException(error, "The button press failed.").message);
				})
				.catch(viaSocket);
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
			<div className="flex min-h-0 flex-1 items-center justify-center overflow-hidden p-3">
				{/* Shut down outside AO: say so rather than hold the last frame. */}
				{device.booted ? null : (
					<div className="flex flex-col items-center gap-2">
						<p className="text-sm text-muted-foreground">{t("device.offLive", { name: device.name })}</p>
						<Button disabled={starting} onClick={onStart} size="sm" type="button" variant="outline">{t("device.start")}</Button>
					</div>
				)}
				{hasFrame || !device.booted ? null : <p className="text-xs text-muted-foreground">{t("device.connecting")}</p>}
				<canvas
						aria-label={t("device.screenAlt", { name: device.name })}
						className={cn("max-h-full max-w-full cursor-pointer touch-none select-none rounded-lg bg-black", (!hasFrame || !device.booted) && "hidden")}
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
