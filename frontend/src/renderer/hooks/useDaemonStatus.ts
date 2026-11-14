import { useEffect, useRef, useState } from "react";
import type { QueryClient } from "@tanstack/react-query";
import { aoBridge } from "../lib/bridge";
import { applyDaemonStatus, readDaemonStatus, type DaemonStatus } from "../lib/daemon-status";
import { queryClient as defaultQueryClient } from "../lib/query-client";
import { createEventTransport } from "../lib/event-transport";
import { getEventsConnectionState, subscribeEventsConnection } from "../lib/events-connection";
import {
	agentReadinessQueryKey,
	cacheAgentReadiness,
	ensureAgentReadiness,
} from "./useAgentReadinessQuery";
import { systemRequirementsQueryKey } from "./useSystemRequirementsGate";

const STATUS_REFRESH_MS = 2_000;
const READY_STATUS_REFRESH_MS = 10_000;

export function sameDaemonStatus(a: DaemonStatus, b: DaemonStatus): boolean {
	const aKeys = Object.keys(a) as (keyof DaemonStatus)[];
	if (aKeys.length !== Object.keys(b).length) return false;
	return aKeys.every((key) => Object.is(a[key], b[key]));
}

export function useDaemonStatus(queryClient: QueryClient = defaultQueryClient) {
	const [status, setStatus] = useState<DaemonStatus>({ state: "stopped" });
	const statusRef = useRef(status);

	useEffect(() => {
		let active = true;
		let stopTransport: () => void = () => undefined;
		let refreshTimer: ReturnType<typeof setTimeout> | undefined;
		let statusVersion = 0;

		const clearRefresh = () => {
			if (refreshTimer) {
				clearTimeout(refreshTimer);
				refreshTimer = undefined;
			}
		};

		const refreshStatus = (): Promise<DaemonStatus | undefined> => {
			clearRefresh();
			const requestVersion = ++statusVersion;
			return readDaemonStatus()
				.then((nextStatus) => {
					if (active && requestVersion === statusVersion) {
						applyStatus(nextStatus);
						return nextStatus;
					}
					return undefined;
				})
				.catch(() => {
					return undefined;
				})
				.finally(() => {
					if (!active || requestVersion !== statusVersion) return;
					scheduleRefresh(statusRef.current.state === "ready" ? READY_STATUS_REFRESH_MS : STATUS_REFRESH_MS);
				});
		};

		const scheduleRefresh = (delayMs = STATUS_REFRESH_MS) => {
			if (refreshTimer || !active) return;
			refreshTimer = setTimeout(refreshStatus, delayMs);
		};

		const applyStatus = (nextStatus: DaemonStatus) => {
			const previousStatus = statusRef.current;
			statusRef.current = nextStatus;
			const daemonChanged =
				nextStatus.state !== "ready" ||
				previousStatus.state !== "ready" ||
				previousStatus.port !== nextStatus.port ||
				previousStatus.pid !== nextStatus.pid;
			if (daemonChanged) {
				queryClient.removeQueries({ queryKey: agentReadinessQueryKey, exact: true });
				queryClient.removeQueries({ queryKey: systemRequirementsQueryKey });
			}
			if (nextStatus.state === "ready" && nextStatus.port) {
				applyDaemonStatus(nextStatus);
				clearRefresh();
				scheduleRefresh(READY_STATUS_REFRESH_MS);
			} else {
				applyDaemonStatus(nextStatus);
				scheduleRefresh();
			}
			if (sameDaemonStatus(previousStatus, nextStatus)) {
				statusRef.current = previousStatus;
				return;
			}
			setStatus(nextStatus);
		};

		void refreshStatus();
		const refreshOnFocus = () => {
			void refreshStatus().then((nextStatus) => {
				if (nextStatus?.state !== "ready") return;
				void ensureAgentReadiness([], "display")
					.then((next) => cacheAgentReadiness(queryClient, next))
					.catch(() => undefined);
			});
		};
		const refreshOnVisibility = () => {
			if (document.visibilityState === "visible") refreshOnFocus();
		};
		window.addEventListener("focus", refreshOnFocus);
		document.addEventListener("visibilitychange", refreshOnVisibility);

		void Promise.resolve().then(() => {
			if (active) stopTransport = createEventTransport(queryClient).connect();
		});

		const stopStatusListener = aoBridge.daemon.onStatus((nextStatus) => {
			statusVersion += 1;
			applyStatus(nextStatus);
		});

		const stopEventsConnectionListener = subscribeEventsConnection(() => {
			if (getEventsConnectionState() === "disconnected" && statusRef.current.state === "ready") {
				void refreshStatus();
			}
		});

		return () => {
			active = false;
			clearRefresh();
			window.removeEventListener("focus", refreshOnFocus);
			document.removeEventListener("visibilitychange", refreshOnVisibility);
			stopTransport();
			stopStatusListener();
			stopEventsConnectionListener();
		};
	}, [queryClient]);

	return status;
}
