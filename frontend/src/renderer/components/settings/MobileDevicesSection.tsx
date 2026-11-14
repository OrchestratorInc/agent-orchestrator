import { apiErrorException } from "../../lib/api-error-exception";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Laptop, Loader2, Smartphone, Trash2, Users } from "@/components/app-icons";
import { apiClient, apiErrorCode, apiErrorMessage } from "../../lib/api-client";
import { usesPreviewWorkspaceData } from "../../lib/preview-mode";
import { Button } from "../ui/button";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";

export const mobileDevicesQueryKey = ["mobile-devices"] as const;

/**
 * Error code the daemon returns from both roster routes (list/remove)
 * when the on-disk device registry (~/.ao/data/mobile/push-devices.json) failed
 * to load — e.g. it's corrupt. This is distinct from "you have no devices": an
 * unreadable registry must be surfaced explicitly, never rendered as the empty
 * state.
 */
const DEVICE_REGISTRY_UNAVAILABLE_CODE = "DEVICE_REGISTRY_UNAVAILABLE";

/**
 * How long a removed row offers Undo before the DELETE is actually sent.
 * Removing a device revokes its access, so the row can be taken back first.
 */
export const REMOVE_UNDO_MS = 2000;

/** device: its own token. password: a phone on the pairing password, which removing
 * only stops notifying. account: every device signed in to the linked account. */
type DeviceKind = "device" | "password" | "account";

interface MobileDevice {
	id: string;
	kind: DeviceKind;
	deviceName?: string;
	platform?: string;
	live: boolean;
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
	{ id: "preview-1", kind: "device", deviceName: "iPhone 17 Pro", platform: "ios", live: true, lastSeenAt: new Date().toISOString() },
	{ id: "preview-2", kind: "device", deviceName: "Work MacBook", platform: "darwin", live: false, lastSeenAt: new Date().toISOString() },
	{ id: "preview-3", kind: "password", deviceName: "iPad Air", platform: "ios", live: false, lastSeenAt: new Date().toISOString() },
	{ id: "account", kind: "account", live: false, lastSeenAt: new Date().toISOString() },
];

export async function fetchDevices(): Promise<MobileDevice[]> {
	if (usesPreviewWorkspaceData) return previewDevices;
	const { data, error } = await apiClient.GET("/api/v1/mobile/devices");
	if (error || !data) throw new MobileDevicesQueryError(apiErrorMessage(error), apiErrorCode(error));
	return data.devices as MobileDevice[];
}

// MobileDevicesSection lists every device that can reach this computer, with
// a remove action that revokes it. Whether a phone gets push notifications is
// decided on the phone itself, not here.
export function MobileDevicesSection() {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	// Rows removed in the UI and shown as "removed · Undo" until their DELETE
	// settles. deletingIds is the subset whose undo window has ended and whose
	// DELETE is in flight, so Undo can no longer cancel it.
	const [pendingRemovals, setPendingRemovals] = useState<ReadonlySet<string>>(() => new Set());
	const [deletingIds, setDeletingIds] = useState<ReadonlySet<string>>(() => new Set());
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

	const remove = useMutation({
		mutationFn: async (id: string) => {
			if (usesPreviewWorkspaceData) {
				previewDevices = previewDevices.filter((d) => d.id !== id);
				return;
			}
			const { error } = await apiClient.DELETE("/api/v1/mobile/devices/{id}", {
				params: { path: { id } },
			});
			if (error) throw apiErrorException(error);
		},
		onSuccess: async (_data, id) => {
			// Drop the row from the cached roster rather than hiding its id, so a
			// later poll that lists it again (the device paired again) shows
			// it. Cancel first so a poll that started before the DELETE landed
			// cannot write the stale row back.
			await queryClient.cancelQueries({ queryKey: mobileDevicesQueryKey });
			queryClient.setQueryData<MobileDevice[]>(mobileDevicesQueryKey, (prev) =>
				prev?.filter((d) => d.id !== id),
			);
			invalidate();
		},
		onSettled: (_data, _error, id) => {
			// On failure the row simply comes back, with the error shown below.
			setPendingRemovals((prev) => without(prev, id));
			setDeletingIds((prev) => without(prev, id));
		},
	});

	// The latest mutate, readable from timers and the unmount cleanup without
	// re-subscribing them every render.
	const removeDevice = useRef(remove.mutate);
	removeDevice.current = remove.mutate;

	const startRemoval = (id: string) => {
		remove.reset();
		setPendingRemovals((prev) => new Set(prev).add(id));
		removalTimers.current.set(
			id,
			setTimeout(() => {
				removalTimers.current.delete(id);
				setDeletingIds((prev) => new Set(prev).add(id));
				removeDevice.current(id);
			}, REMOVE_UNDO_MS),
		);
	};

	const undoRemoval = (id: string) => {
		const timer = removalTimers.current.get(id);
		// The window already closed and the DELETE is on its way; restoring the
		// row now would show a device that is about to be gone.
		if (timer === undefined) return;
		clearTimeout(timer);
		removalTimers.current.delete(id);
		setPendingRemovals((prev) => without(prev, id));
		// Hand focus back to the row's trash button, which replaces Undo.
		requestAnimationFrame(() => trashButtons.current.get(id)?.focus());
	};

	// Closing settings mid-countdown commits the removal rather than dropping it:
	// the row already looked gone, so silently keeping the device would surprise.
	useEffect(() => {
		const timers = removalTimers.current;
		return () => {
			for (const [id, timer] of timers) {
				clearTimeout(timer);
				removeDevice.current(id);
			}
			timers.clear();
		};
	}, []);

	const devices = query.data ?? [];
	// Stable client-side order: the daemon sorts live-first then by LastSeenAt
	// descending, and LastSeenAt advances on every phone poll — with 2+ live
	// devices that ordering can flip on any 3s refetch, jumping rows under the
	// cursor mid-interaction (e.g. right as someone reaches for Undo).
	// id never changes for a paired device, so sorting on it keeps row
	// order fixed across polls regardless of what the server returns. The
	// account's shared row stays last.
	const sortedDevices = [...devices].sort((a, b) => Number(a.kind === "account") - Number(b.kind === "account") || a.id.localeCompare(b.id));
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
	const mutationError = remove.error instanceof Error ? remove.error.message : null;

	return (
		<SettingsSection title={t("mobile.devices.title")} grouped>
			{query.isLoading ? (
				<div className="settings-row-bar text-caption text-settings-muted">
					<span className="flex items-center gap-2">
						<Loader2 className="size-3 animate-spin" /> {t("mobile.devices.loading")}
					</span>
				</div>
			) : registryUnavailable ? (
				<div className="settings-row-bar text-caption text-error">{t("mobile.devices.registryUnavailable")}</div>
			) : queryError && !hasData ? (
				<div className="settings-row-bar text-caption text-error">{queryError.message}</div>
			) : devices.length === 0 ? (
				<div className="settings-row-bar text-caption text-settings-muted">{t("mobile.devices.empty")}</div>
			) : (
				<>
					{queryError && <div className="settings-row-bar text-caption text-error">{queryError.message}</div>}
					<div role="list" className="divide-y divide-border">
					{sortedDevices.map((device) => {
						const name = device.kind === "account" ? t("mobile.devices.account") : device.deviceName || t("mobile.devices.unnamed");
						const icon = device.kind === "account" ? Users : isPhone(device) ? Smartphone : Laptop;
						// Only rows where removing does not simply revoke one device get a description.
						const description = device.kind === "account" ? t("mobile.devices.accountDescription")
							: device.kind === "password" ? t("mobile.devices.passwordDescription") : undefined;
						if (pendingRemovals.has(device.id)) {
							return (
								<SettingsRow
									// Its own key so the row remounts: the Undo button would otherwise
									// reuse the trash button's element and autoFocus would never fire.
									key={`${device.id}:removed`}
									role="listitem"
									className="relative"
									icon={icon}
									label={
										<span className="text-settings-muted" role="status">
											{t("mobile.devices.removed", { name })}
										</span>
									}
								>
									<Button
										type="button"
										variant="ghost"
										// The trash button this replaces had focus; keep it on the row.
										autoFocus
										aria-label={t("mobile.devices.undoRemoveAria", { name })}
										className="px-2 font-medium text-primary"
										disabled={deletingIds.has(device.id)}
										onClick={() => undoRemoval(device.id)}
									>
										{t("mobile.devices.undo")}
									</Button>
									<span
										className="mobile-device-undo-countdown"
										style={{ animationDuration: `${REMOVE_UNDO_MS}ms` }}
										aria-hidden="true"
									/>
								</SettingsRow>
							);
						}
						return (
							<SettingsRow key={device.id} role="listitem" icon={icon} label={name} description={description}>
								<Button
									type="button"
									variant="destructive-ghost"
									size="icon"
									ref={(el) => {
										if (el) trashButtons.current.set(device.id, el);
										else trashButtons.current.delete(device.id);
									}}
									aria-label={t("mobile.devices.removeAria", { name })}
									title={t("mobile.devices.removeAria", { name })}
									onClick={() => startRemoval(device.id)}
								>
									<Trash2 className="size-4" />
								</Button>
							</SettingsRow>
						);
					})}
					</div>
				</>
			)}

			{mutationError && <div className="settings-row-bar text-caption text-error">{mutationError}</div>}
		</SettingsSection>
	);
}

const desktopPlatforms = new Set(["darwin", "win32", "linux"]);

/** Whether a row is one phone, not a computer or the account's shared row. */
export function isPhone(device: Pick<MobileDevice, "kind" | "platform">): boolean {
	return device.kind !== "account" && !desktopPlatforms.has(device.platform ?? "");
}

function without(set: ReadonlySet<string>, id: string): ReadonlySet<string> {
	if (!set.has(id)) return set;
	const next = new Set(set);
	next.delete(id);
	return next;
}
