import { apiErrorException } from "../lib/api-error-exception";
import { useIsMutating, useMutation, useMutationState, useQuery, useQueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";
import { mobileStatusQueryKey } from "../lib/mobile-status-key";
import { awakeCapability, awakeQueryKey, type AwakeState, useAwake } from "./useAwake";

type MobileStatus = components["schemas"]["MobileStatusResponse"];
type Change = { on: boolean; lidClosed?: boolean; rememberMobile?: boolean; idleFallback?: boolean };
const mutationKey = ["stay-awake-toggle"] as const;

export function useStayAwake(enabled = true) {
	const cache = useQueryClient();
	const awake = useAwake(enabled);
	const mobile = useQuery({
		queryKey: mobileStatusQueryKey,
		enabled,
		queryFn: async ({ signal }) => {
			const { data, error } = await apiClient.GET("/api/v1/mobile/status", { signal });
			if (error) throw apiErrorException(error);
			if (!data) throw new Error("Mobile status is unavailable");
			return data;
		},
		staleTime: 60_000,
		refetchOnWindowFocus: true,
		refetchInterval: false,
		retry: false,
	});
	const sessions = awake.state?.sessions?.filter((s) => !s.endedAt) ?? [];
	const rules = awake.state?.paused ? [] : awake.state?.rules?.filter((r) => r.enabled && !r.suppressed) ?? [];
	const desktopOn = sessions.length > 0 || rules.length > 0 || !!awake.state?.active;
	const desktopLid = sessions.some((s) => s.lidClosed) || rules.some((r) => r.lidClosed) || !!awake.state?.lidActive;
	const pending = useMutationState<Change>({
		filters: { mutationKey, status: "pending" },
		select: (m) => m.state.variables as Change,
	});
	const intent = pending.at(-1);
	const busy = useIsMutating({ mutationKey }) > 0 || awake.busy;
	const active = !!awake.state?.active || !!mobile.data?.keepAwake?.active;
	const on = intent?.on ?? (desktopOn || !!mobile.data?.keepAwake?.enabled || active);
	const pauseReason = awake.state?.paused ? awake.state.reason : undefined;
	const reason = pauseReason === "low_battery" || pauseReason === "critical_heat" || pauseReason === "battery_unknown"
		? pauseReason : undefined;
	const latestChange = () => cache.getMutationCache().findAll({ mutationKey, status: "pending" }).at(-1)?.state.variables as Change | undefined;
	const currentState = () => cache.getQueryData<AwakeState>(awakeQueryKey) ?? awake.state;
	const mutation = useMutation({
		mutationKey,
		scope: { id: "stay-awake-toggle" },
		mutationFn: async (change: Change) => {
			const isLatest = () => latestChange() === change;
			if (!isLatest()) return;
			await cache.cancelQueries({ queryKey: mobileStatusQueryKey });
			if (!isLatest()) return;
			const current = currentState();
			const currentLid = current?.sessions?.some((s) => !s.endedAt && s.lidClosed)
				|| (!current?.paused && current?.rules?.some((r) => r.enabled && !r.suppressed && r.lidClosed)) || current?.lidActive;
			const updateMobile = async (on: boolean, lidClosed: boolean) => {
				const { data, error } = await apiClient.POST("/api/v1/mobile/keep-awake", { body: { enabled: on, lidClosed } });
				if (error) throw apiErrorException(error);
				if (!data) throw new Error("Mobile status is unavailable");
				cache.setQueryData<MobileStatus>(mobileStatusQueryKey, data);
			};
			const confirmLid = async () => {
				const deadline = Date.now() + 5_000;
				while (isLatest() && cache.getQueryData<MobileStatus>(mobileStatusQueryKey)?.keepAwake?.lidReason === "starting" && Date.now() < deadline) {
					await new Promise((resolve) => setTimeout(resolve, 500));
					try {
						const { data } = await apiClient.GET("/api/v1/mobile/status");
						if (!data) break;
						cache.setQueryData<MobileStatus>(mobileStatusQueryKey, data);
					} catch {
						break;
					}
				}
			};
			const start = async () => {
				let latest = await awake.actAsync({ kind: "start", request: { scope: "ao", sessionId: "", mode: "indefinite", lidClosed: false } });
				const deadline = Date.now() + 5_000;
				while (isLatest() && !latest.active && !latest.paused && (!latest.reason || latest.reason === "starting" || latest.reason === "helper_unavailable") && Date.now() < deadline) {
					await new Promise((resolve) => setTimeout(resolve, 500));
					let timeout: number | undefined;
					try {
						const confirmed = await Promise.race([
							cache.fetchQuery<AwakeState>({ queryKey: awakeQueryKey, staleTime: 0 }),
							new Promise<undefined>((resolve) => { timeout = window.setTimeout(resolve, Math.max(0, deadline - Date.now())); }),
						]);
						if (!confirmed) break;
						latest = confirmed;
					} catch {
						break;
					} finally {
						window.clearTimeout(timeout);
					}
				}
			};
			const ensureDesktop = async () => {
				if (currentState()?.paused) await awake.actAsync({ kind: "resume" });
				if (currentState()?.sessions?.some((s) => !s.endedAt)) return;
				try {
					await start();
				} catch (e) {
					await cache.cancelQueries({ queryKey: awakeQueryKey });
					const latest = await cache.fetchQuery<AwakeState>({ queryKey: awakeQueryKey, staleTime: 0 }).catch(() => undefined);
					if (!latest?.paused) throw e;
					await awake.actAsync({ kind: "resume" });
					await start();
				}
			};
			if (!change.on) {
				const results = await Promise.allSettled([awake.actAsync({ kind: "stop" }), updateMobile(false, false)]);
				for (const result of results) if (result.status === "rejected") throw result.reason;
			} else if (change.lidClosed === false) {
				if (currentLid) {
					const results = await Promise.allSettled([awake.actAsync({ kind: "stop" }), updateMobile(true, false)]);
					for (const result of results) if (result.status === "rejected") throw result.reason;
					await start();
				} else await updateMobile(true, false);
			} else if (change.lidClosed === true) {
				try {
					await updateMobile(true, true);
				} catch (e) {
					if (!change.idleFallback) throw e;
					await ensureDesktop();
					return;
				}
				await ensureDesktop();
				await confirmLid();
			} else {
				await ensureDesktop();
				if (change.rememberMobile) await updateMobile(true, false);
			}
		},
	});
	const change = (next: Change) => {
		mutation.mutate(next);
	};
	return {
		on,
		active,
		attention: on && !active && !busy,
		reason: desktopOn ? reason : undefined,
		stopBatteryPercent: awake.state?.settings?.stopBatteryPercent ?? 10,
		lidOn: intent ? intent.on && (intent.lidClosed ?? false) : desktopLid || !!(mobile.data?.keepAwake?.enabled && mobile.data.keepAwake.lidClosed),
		toggle: () => {
			const current = currentState();
			const mobileState = cache.getQueryData<MobileStatus>(mobileStatusQueryKey);
			const requested = current?.sessions?.some((s) => !s.endedAt)
				|| (!current?.paused && current?.rules?.some((r) => r.enabled && !r.suppressed))
				|| current?.active || mobileState?.keepAwake?.enabled || mobileState?.keepAwake?.active;
			const on = !(latestChange()?.on ?? requested);
			change(on && mobileState?.keepAwake?.lidSupported ? { on, lidClosed: true, idleFallback: true } : { on });
		},
		set: (on: boolean, rememberMobile = false) => change({ on, rememberMobile }),
		setLid: (lidClosed: boolean) => change({ on: true, lidClosed }),
		busy,
		error: mutation.error instanceof Error ? mutation.error.message : null,
		supported: awakeCapability(awake.state, "idle")?.available ?? false,
		starting: intent?.on === true,
		stopping: intent?.on === false,
	};
}
