import { useIsMutating, useMutation, useMutationState, useQuery, useQueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { mobileStatusQueryKey } from "../lib/mobile-status-key";
import { awakeCapability, awakeQueryKey, type AwakeState, useAwake } from "./useAwake";

type MobileStatus = components["schemas"]["MobileStatusResponse"];
type Change = { on: boolean; lidClosed?: boolean; rememberMobile?: boolean };
const mutationKey = ["stay-awake-toggle"] as const;

/** The desktop and mobile controls share intent, pending state, and ownership. */
export function useStayAwake(enabled = true) {
	const cache = useQueryClient();
	const awake = useAwake(enabled);
	const mobile = useQuery({
		queryKey: mobileStatusQueryKey,
		enabled,
		queryFn: async ({ signal }) => {
			const { data, error } = await apiClient.GET("/api/v1/mobile/status", { signal });
			if (error) throw new Error(apiErrorMessage(error));
			if (!data) throw new Error("Mobile status is unavailable");
			return data;
		},
		// Status can spawn host probes; the sidebar only refreshes stale data on
		// mount/focus. Connect Mobile owns its separate pairing-pending interval.
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
	// Requested ownership must stay clearable even when a rule is waiting or a
	// safety stop has released the host assertion. Actual wake is separate.
	const active = !!awake.state?.active || !!mobile.data?.keepAwake?.active;
	const on = intent?.on ?? (desktopOn || !!mobile.data?.keepAwake?.enabled || active);
	// Status also includes recovery/lid errors unrelated to an inactive request.
	const pauseReason = awake.state?.paused ? awake.state.reason : undefined;
	const reason = pauseReason === "low_battery" || pauseReason === "critical_heat" || pauseReason === "battery_unknown"
		? pauseReason : undefined;
	const mutation = useMutation({
		mutationKey,
		mutationFn: async (change: Change) => {
			// Cancel an older status read before writing the authoritative response.
			await cache.cancelQueries({ queryKey: mobileStatusQueryKey });
			const updateMobile = async (on: boolean, lidClosed: boolean) => {
				const { data, error } = await apiClient.POST("/api/v1/mobile/keep-awake", { body: { enabled: on, lidClosed } });
				if (error) throw new Error(apiErrorMessage(error));
				if (!data) throw new Error("Mobile status is unavailable");
				cache.setQueryData<MobileStatus>(mobileStatusQueryKey, data);
			};
			const start = async () => {
				let latest = await awake.actAsync({ kind: "start", request: { scope: "ao", sessionId: "", mode: "indefinite", lidClosed: false } });
				// A successful POST writes a lease; the native helper acknowledges it
				// on its next two-second scan. Keep the shared intent pending until
				// that acknowledgement instead of flashing an inactive warning.
				const deadline = Date.now() + 5_000;
				while (!latest.active && !latest.paused && (!latest.reason || latest.reason === "starting" || latest.reason === "helper_unavailable") && Date.now() < deadline) {
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
						// An unavailable status endpoint must not leave the controls busy.
						break;
					} finally {
						window.clearTimeout(timeout);
					}
				}
			};
			const ensureDesktop = async () => {
				if (awake.state?.paused) await awake.actAsync({ kind: "resume" });
				if (sessions.length > 0) return;
				try {
					await start();
				} catch (e) {
					// A safety stop can happen after the last poll. Re-read the daemon's
					// structured pause state rather than interpreting its error wording.
					await cache.cancelQueries({ queryKey: awakeQueryKey });
					const latest = await cache.fetchQuery<AwakeState>({ queryKey: awakeQueryKey, staleTime: 0 }).catch(() => undefined);
					if (!latest?.paused) throw e;
					await awake.actAsync({ kind: "resume" });
					await start();
				}
			};
			if (!change.on) {
				// Stop all scopes and pause automatic rules, including pre-upgrade owners.
				// Attempt both releases even if one backend fails.
				const results = await Promise.allSettled([awake.actAsync({ kind: "stop" }), updateMobile(false, false)]);
				for (const result of results) if (result.status === "rejected") throw result.reason;
			} else if (change.lidClosed === false) {
				if (desktopLid) {
					// Retire legacy desktop lid owners and replace them with idle-only wake.
					const results = await Promise.allSettled([awake.actAsync({ kind: "stop" }), updateMobile(true, false)]);
					for (const result of results) if (result.status === "rejected") throw result.reason;
					await start();
				} else await updateMobile(true, false);
			} else {
				await ensureDesktop();
				// Main toggle is idle-only. Closed-lid wake requires the explicit option.
				if (change.lidClosed === true || change.rememberMobile) await updateMobile(true, change.lidClosed === true);
			}
		},
	});
	const change = (next: Change) => {
		// The cache guard also covers two calls before React renders the disabled UI.
		if (cache.isMutating({ mutationKey }) > 0 || awake.busy) return;
		mutation.mutate(next);
	};
	return {
		on,
		active,
		attention: on && !active && !busy,
		reason: desktopOn ? reason : undefined,
		lidOn: intent ? intent.on && (intent.lidClosed ?? false) : desktopLid || !!(mobile.data?.keepAwake?.enabled && mobile.data.keepAwake.lidClosed),
		set: (on: boolean, rememberMobile = false) => change({ on, rememberMobile }),
		setLid: (lidClosed: boolean) => change({ on: true, lidClosed }),
		busy,
		error: mutation.error instanceof Error ? mutation.error.message : null,
		supported: awakeCapability(awake.state, "idle")?.available ?? false,
		starting: intent?.on === true && !active,
	};
}
