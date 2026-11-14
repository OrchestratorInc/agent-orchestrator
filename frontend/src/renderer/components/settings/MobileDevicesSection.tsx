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

const DEVICE_REGISTRY_UNAVAILABLE_CODE = "DEVICE_REGISTRY_UNAVAILABLE";

export const REMOVE_UNDO_MS = 2000;

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

export function MobileDevicesSection() {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
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
			await queryClient.cancelQueries({ queryKey: mobileDevicesQueryKey });
			queryClient.setQueryData<MobileDevice[]>(mobileDevicesQueryKey, (prev) =>
				prev?.filter((d) => d.id !== id),
			);
			invalidate();
		},
		onSettled: (_data, _error, id) => {
			setPendingRemovals((prev) => without(prev, id));
			setDeletingIds((prev) => without(prev, id));
		},
	});

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
		if (timer === undefined) return;
		clearTimeout(timer);
		removalTimers.current.delete(id);
		setPendingRemovals((prev) => without(prev, id));
		requestAnimationFrame(() => trashButtons.current.get(id)?.focus());
	};

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
	const sortedDevices = [...devices].sort((a, b) => Number(a.kind === "account") - Number(b.kind === "account") || a.id.localeCompare(b.id));
	const queryError = query.error as MobileDevicesQueryError | null;
	const registryUnavailable = queryError?.code === DEVICE_REGISTRY_UNAVAILABLE_CODE;
	const hasData = query.data !== undefined;

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
						const description = device.kind === "account" ? t("mobile.devices.accountDescription")
							: device.kind === "password" ? t("mobile.devices.passwordDescription") : undefined;
						if (pendingRemovals.has(device.id)) {
							return (
								<SettingsRow
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

export function isPhone(device: Pick<MobileDevice, "kind" | "platform">): boolean {
	return device.kind !== "account" && !desktopPlatforms.has(device.platform ?? "");
}

function without(set: ReadonlySet<string>, id: string): ReadonlySet<string> {
	if (!set.has(id)) return set;
	const next = new Set(set);
	next.delete(id);
	return next;
}
