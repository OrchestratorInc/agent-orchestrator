import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Bell, Loader2, Smartphone, Trash2 } from "lucide-react";
import { apiClient, apiErrorCode, apiErrorMessage } from "../../lib/api-client";
import { usesPreviewWorkspaceData } from "../../lib/preview-mode";
import { Switch } from "../ui/switch";

export const mobileDevicesQueryKey = ["mobile-devices"] as const;

/**
 * Error code the daemon returns from all three roster routes (list/mute/remove)
 * when the on-disk device registry (~/.ao/data/mobile/push-devices.json) failed
 * to load — e.g. it's corrupt. This is distinct from "you have no devices": an
 * unreadable registry must be surfaced explicitly, never rendered as the empty
 * state.
 */
const DEVICE_REGISTRY_UNAVAILABLE_CODE = "DEVICE_REGISTRY_UNAVAILABLE";

/**
 * How long a removed row offers Undo before the DELETE is actually sent. Removal
 * is soft on the daemon side (a re-registering phone comes back muted), so an
 * undo window replaces a separate confirm step.
 */
export const REMOVE_UNDO_MS = 2000;

interface MobileDevice {
	installId: string;
	deviceName?: string;
	platform?: string;
	muted: boolean;
	live: boolean;
	notificationsEnabled: boolean;
	lastSeenAt: string;
}

class MobileDevicesQueryError extends Error {
	code?: string;

	constructor(message: string, code?: string) {
		super(message);
		this.code = code;
	}
}

// The web preview build (dev:web) has no daemon, so it manages a fixed set of
// sample phones in memory instead of hitting the real roster.
let previewDevices: MobileDevice[] = [
	{ installId: "preview-1", deviceName: "iPhone 17 Pro", platform: "ios", muted: false, live: true, notificationsEnabled: true, lastSeenAt: new Date().toISOString() },
	{ installId: "preview-2", deviceName: "Pixel 10", platform: "android", muted: true, live: false, notificationsEnabled: true, lastSeenAt: new Date().toISOString() },
	{ installId: "preview-3", deviceName: "iPad Air", platform: "ios", muted: false, live: false, notificationsEnabled: false, lastSeenAt: new Date().toISOString() },
];

export async function fetchDevices(): Promise<MobileDevice[]> {
	if (usesPreviewWorkspaceData) return previewDevices;
	const { data, error } = await apiClient.GET("/api/v1/mobile/devices");
	if (error || !data) throw new MobileDevicesQueryError(apiErrorMessage(error), apiErrorCode(error));
	return data.devices as MobileDevice[];
}

// MobileDevicesSection lists every paired phone with whether its app is open right
// now, a per-device mute switch, and a remove action. Live status comes from the
// daemon's presence tracker, which is fed by each phone's own REST poll.
export function MobileDevicesSection() {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	// Rows removed in the UI and shown as "removed · Undo" until their DELETE
	// settles. deletingIds is the subset whose undo window has ended and whose
	// DELETE is in flight, so Undo can no longer cancel it.
	const [pendingRemovals, setPendingRemovals] = useState<ReadonlySet<string>>(() => new Set());
	const [deletingIds, setDeletingIds] = useState<ReadonlySet<string>>(() => new Set());
	// Devices with a mute PATCH in flight. Tracked per device because the
	// mutation's own isPending/variables only describe the latest call, so a
	// second toggle would otherwise unlock the first device's switch early.
	const [mutingIds, setMutingIds] = useState<ReadonlySet<string>>(() => new Set());
	const removalTimers = useRef(new Map<string, ReturnType<typeof setTimeout>>());
	const trashButtons = useRef(new Map<string, HTMLButtonElement>());

	const query = useQuery({
		queryKey: mobileDevicesQueryKey,
		queryFn: fetchDevices,
		refetchInterval: 3000,
	});

	const invalidate = () => {
		void queryClient.invalidateQueries({ queryKey: mobileDevicesQueryKey });
	};

	const mute = useMutation({
		mutationFn: async ({ installId, muted }: { installId: string; muted: boolean }) => {
			if (usesPreviewWorkspaceData) {
				previewDevices = previewDevices.map((d) => (d.installId === installId ? { ...d, muted } : d));
				return;
			}
			const { error } = await apiClient.PATCH("/api/v1/mobile/devices/{installId}", {
				params: { path: { installId } },
				body: { muted },
			});
			if (error) throw new Error(apiErrorMessage(error));
		},
		onMutate: ({ installId }) => {
			setMutingIds((prev) => new Set(prev).add(installId));
		},
		onSuccess: invalidate,
		onSettled: (_data, _error, { installId }) => {
			setMutingIds((prev) => without(prev, installId));
		},
	});

	const remove = useMutation({
		mutationFn: async (installId: string) => {
			if (usesPreviewWorkspaceData) {
				previewDevices = previewDevices.filter((d) => d.installId !== installId);
				return;
			}
			const { error } = await apiClient.DELETE("/api/v1/mobile/devices/{installId}", {
				params: { path: { installId } },
			});
			if (error) throw new Error(apiErrorMessage(error));
		},
		onSuccess: async (_data, installId) => {
			// Drop the row from the cached roster rather than hiding its id, so a
			// later poll that lists it again (the phone re-registered, muted) shows
			// it. Cancel first so a poll that started before the DELETE landed
			// cannot write the stale row back.
			await queryClient.cancelQueries({ queryKey: mobileDevicesQueryKey });
			queryClient.setQueryData<MobileDevice[]>(mobileDevicesQueryKey, (prev) =>
				prev?.filter((d) => d.installId !== installId),
			);
			invalidate();
		},
		onSettled: (_data, _error, installId) => {
			// On failure the row simply comes back, with the error shown below.
			setPendingRemovals((prev) => without(prev, installId));
			setDeletingIds((prev) => without(prev, installId));
		},
	});

	// The latest mutate, readable from timers and the unmount cleanup without
	// re-subscribing them every render.
	const removeDevice = useRef(remove.mutate);
	removeDevice.current = remove.mutate;

	const startRemoval = (installId: string) => {
		remove.reset();
		setPendingRemovals((prev) => new Set(prev).add(installId));
		removalTimers.current.set(
			installId,
			setTimeout(() => {
				removalTimers.current.delete(installId);
				setDeletingIds((prev) => new Set(prev).add(installId));
				removeDevice.current(installId);
			}, REMOVE_UNDO_MS),
		);
	};

	const undoRemoval = (installId: string) => {
		const timer = removalTimers.current.get(installId);
		// The window already closed and the DELETE is on its way; restoring the
		// row now would show a device that is about to be gone.
		if (timer === undefined) return;
		clearTimeout(timer);
		removalTimers.current.delete(installId);
		setPendingRemovals((prev) => without(prev, installId));
		// Hand focus back to the row's trash button, which replaces Undo.
		requestAnimationFrame(() => trashButtons.current.get(installId)?.focus());
	};

	// Closing settings mid-countdown commits the removal rather than dropping it:
	// the row already looked gone, so silently keeping the device would surprise.
	useEffect(() => {
		const timers = removalTimers.current;
		return () => {
			for (const [installId, timer] of timers) {
				clearTimeout(timer);
				removeDevice.current(installId);
			}
			timers.clear();
		};
	}, []);

	const devices = query.data ?? [];
	// Stable client-side order: the daemon sorts live-first then by LastSeenAt
	// descending, and LastSeenAt advances on every phone poll — with 2+ live
	// devices that ordering can flip on any 3s refetch, jumping rows under the
	// cursor mid-interaction (e.g. right as someone reaches for Undo).
	// installId never changes for a paired device, so sorting on it keeps row
	// order fixed across polls regardless of what the server returns.
	const sortedDevices = [...devices].sort((a, b) => a.installId.localeCompare(b.installId));
	const queryError = query.error as MobileDevicesQueryError | null;
	const registryUnavailable = queryError?.code === DEVICE_REGISTRY_UNAVAILABLE_CODE;
	// A transient poll failure (daemon restart mid-refetch, a one-off 500) should
	// not blank a list we already successfully loaded — that flickers the whole
	// section red and back every time. Only replace the section outright when
	// there is no retained data to show. DEVICE_REGISTRY_UNAVAILABLE always takes
	// over the section regardless of stale data — that state is distinct enough
	// (an unreadable on-disk registry) that showing a stale list next to it would
	// be misleading.
	const hasData = query.data !== undefined;

	// No paired devices (or still loading with nothing cached) → no section at
	// all. Errors and an unreadable registry still render so they stay visible.
	if (!registryUnavailable && !queryError && devices.length === 0) return null;
	const mutationError =
		(mute.error instanceof Error && mute.error.message) ||
		(remove.error instanceof Error && remove.error.message) ||
		null;

	return (
		<section className="mt-6">
			<h3 className="text-sm font-medium text-settings-label">{t("mobile.devices.title")}</h3>

			{query.isLoading ? (
				<div className="mt-3 flex items-center gap-2 text-caption text-settings-muted">
					<Loader2 className="size-3 animate-spin" /> {t("mobile.devices.loading")}
				</div>
			) : registryUnavailable ? (
				<p className="mt-3 text-caption text-error">{t("mobile.devices.registryUnavailable")}</p>
			) : queryError && !hasData ? (
				<p className="mt-3 text-caption text-error">{queryError.message}</p>
			) : devices.length === 0 ? (
				<p className="mt-3 text-caption text-settings-muted">{t("mobile.devices.empty")}</p>
			) : (
				<>
					{queryError && <p className="mt-3 text-caption text-error">{queryError.message}</p>}
					<ul className="mt-2 divide-y divide-[var(--color-border-settings-input)]">
						{sortedDevices.map((device) => {
							const name = device.deviceName || t("mobile.devices.unnamed");
							if (pendingRemovals.has(device.installId)) {
								return (
									<li
										key={device.installId}
										className="relative flex min-h-12 items-center gap-3 py-2.5"
									>
										<Smartphone className="size-4 shrink-0 text-settings-muted opacity-50" aria-hidden="true" />
										<div className="min-w-0 flex-1 truncate text-sm text-settings-muted" role="status">
											{t("mobile.devices.removed", { name })}
										</div>
										<button
											type="button"
											// The trash button this replaces had focus; keep it on the row.
											autoFocus
											aria-label={t("mobile.devices.undoRemoveAria", { name })}
											className="min-h-10 rounded-md px-2 text-sm font-medium text-primary transition-colors hover:bg-interactive-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
											disabled={deletingIds.has(device.installId)}
											onClick={() => undoRemoval(device.installId)}
										>
											{t("mobile.devices.undo")}
										</button>
										<span
											className="mobile-device-undo-countdown"
											style={{ animationDuration: `${REMOVE_UNDO_MS}ms` }}
											aria-hidden="true"
										/>
									</li>
								);
							}
							const tokenless = !device.notificationsEnabled;
							const notificationsTitle = tokenless
								? t("mobile.devices.notificationsOffOnPhone", { name })
								: t("mobile.devices.notificationsFor", { name });
							return (
								<li
									key={device.installId}
									className="flex min-h-12 items-center gap-3 py-2.5"
								>
									<Smartphone className="size-4 shrink-0 text-settings-muted" aria-hidden="true" />
									<div className="min-w-0 flex-1">
										<div className="truncate text-sm">{name}</div>
									</div>

									<div className="flex items-center gap-2" title={notificationsTitle}>
										<Bell className="size-4 text-settings-muted" aria-hidden="true" data-testid="bell" />
										<Switch
											checked={device.notificationsEnabled && !device.muted}
											disabled={tokenless || mutingIds.has(device.installId)}
											aria-label={t("mobile.devices.notificationsFor", { name })}
											onCheckedChange={(next) =>
												mute.mutate({ installId: device.installId, muted: !next })
											}
										/>
									</div>

									<button
										type="button"
										ref={(el) => {
											if (el) trashButtons.current.set(device.installId, el);
											else trashButtons.current.delete(device.installId);
										}}
										aria-label={t("mobile.devices.removeAria", { name })}
										title={t("mobile.devices.removeAria", { name })}
										className="grid size-10 place-items-center rounded-md text-settings-muted transition-colors hover:bg-interactive-hover hover:text-error focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
										onClick={() => startRemoval(device.installId)}
									>
										<Trash2 className="size-4" />
									</button>
								</li>
							);
						})}
					</ul>
				</>
			)}

			{mutationError && <p className="mt-2 text-caption text-error">{mutationError}</p>}
		</section>
	);
}

function without(set: ReadonlySet<string>, id: string): ReadonlySet<string> {
	if (!set.has(id)) return set;
	const next = new Set(set);
	next.delete(id);
	return next;
}
