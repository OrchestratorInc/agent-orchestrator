import { CloudApiError } from "@aoagents/cloud-client";
import { useCallback, useEffect, useRef, useState } from "react";
import { useFocusEffect } from "expo-router";
import { AppState } from "react-native";
import type { SessionSource } from "../environment/types";
import type { TurnSettings } from "../chat/types";
import { mobileInterfaceTransitionIsActive, mobileInterfaceTransitionIsCancellable } from "../session/interfaceTransition";
import { cloudInterfacePollDelay, type CloudInterfaceStatus } from "./interfaceTransition";
import { cloudRequestErrorCopy } from "./errorCopy";

export function useCloudInterfaceTransition(source: SessionSource | undefined, sessionId: string, onSettled?: () => void | Promise<void>) {
	const ownerKey = source?.kind === "cloud" && source.scopeKey ? JSON.stringify([source.scopeKey, sessionId]) : "";
	const live = useRef({ source, ownerKey }); live.current = { source, ownerKey };
	const [owned, setOwned] = useState<{ key: string; status?: CloudInterfaceStatus }>({ key: ownerKey });
	const status = owned.key === ownerKey ? owned.status : undefined;
	const statusRef = useRef(status); statusRef.current = status;
	const [visible, setVisible] = useState(false);
	const [loading, setLoading] = useState(Boolean(ownerKey));
	const [starting, setStarting] = useState(false);
	const [startingPolicy, setStartingPolicy] = useState<"drain" | "interrupt">();
	const [cancelling, setCancelling] = useState(false);
	const [acknowledgingNotice, setAcknowledgingNotice] = useState(false);
	const [acknowledgeNoticeError, setAcknowledgeNoticeError] = useState<string>();
	const [error, setError] = useState<string>();
	const [failure, setFailure] = useState<{ count: number; status?: number; retryAfterSeconds?: number }>({ count: 0 });
	const requests = useRef(new Set<AbortController>());
	const readController = useRef<AbortController | undefined>(undefined);
	const settled = useRef("");
	const busy = useRef(false);
	const readiness = useRef({ code: "", count: 0 });
	const onSettledRef = useRef(onSettled); onSettledRef.current = onSettled;
	const owns = useCallback(() => Boolean(ownerKey && live.current.ownerKey === ownerKey && live.current.source === source), [ownerKey, source]);
	const controllerFor = useCallback(() => { const controller = new AbortController(); requests.current.add(controller); return controller; }, []);
	const refresh = useCallback(async () => {
		if (!owns() || !source?.getInterfaceTransition) return undefined;
		readController.current?.abort();
		const controller = controllerFor(); readController.current = controller;
		try {
			const next = await source.getInterfaceTransition(sessionId, { signal: controller.signal });
			if (!owns() || controller.signal.aborted) return { ok: true as const, status: next, stale: true };
			statusRef.current = next; setOwned({ key: ownerKey, status: next }); setError(undefined); setFailure({ count: 0 });
			const code = next.reasonCode ?? "";
			readiness.current = code === readiness.current.code ? { code, count: readiness.current.count + 1 } : { code, count: 0 };
			if (next.transition && !mobileInterfaceTransitionIsActive(next.transition) && settled.current !== next.transition.id) {
				settled.current = next.transition.id; await onSettledRef.current?.();
			}
			return { ok: true as const, status: next };
		} catch (cause) {
			if (!owns() || controller.signal.aborted) return undefined;
			const message = cloudRequestErrorCopy(cause);
			const httpStatus = cause instanceof CloudApiError ? cause.status : undefined;
			setError(message); setFailure((old) => ({ count: old.count + 1, status: httpStatus, retryAfterSeconds: cause instanceof CloudApiError ? cause.retryAfterSeconds : undefined }));
			return { ok: false as const, error: message, status: httpStatus };
		} finally { requests.current.delete(controller); if (owns() && !controller.signal.aborted) setLoading(false); }
	}, [controllerFor, ownerKey, owns, sessionId, source]);

	useFocusEffect(useCallback(() => {
		if (!ownerKey) return;
		const stop = () => { for (const request of requests.current) request.abort(); requests.current.clear(); busy.current = false; setStarting(false); setStartingPolicy(undefined); setVisible(false); };
		const update = (state: string) => {
			stop();
			if (state === "active") { setVisible(true); setStarting(false); setCancelling(false); setAcknowledgingNotice(false); void refresh(); }
		};
		update(AppState.currentState);
		const subscription = AppState.addEventListener("change", update);
		return () => { subscription.remove(); stop(); };
	}, [ownerKey, refresh]));
	useEffect(() => {
		if (!visible || !ownerKey) return;
		const delay = cloudInterfacePollDelay({ status, failures: failure.count, httpStatus: failure.status, retryAfterSeconds: failure.retryAfterSeconds, readinessAttempts: readiness.current.count });
		if (delay === undefined) return;
		const timer = setTimeout(() => void refresh(), delay); return () => clearTimeout(timer);
	}, [failure, ownerKey, refresh, status, visible]);
	useEffect(() => {
		setOwned({ key: ownerKey }); setLoading(Boolean(ownerKey)); setError(undefined); setFailure({ count: 0 });
		settled.current = ""; readiness.current = { code: "", count: 0 };
	}, [ownerKey]);

	const start = useCallback(async (targetMode: "chat" | "tui", policy: "drain" | "interrupt", settings?: TurnSettings) => {
		if (!owns() || !source?.startInterfaceTransition) throw new Error("Cloud interface switching is unavailable.");
		if (busy.current) return; busy.current = true; setStarting(true); setStartingPolicy(policy); setError(undefined);
		readController.current?.abort();
		const controller = controllerFor();
		try {
			const input = { targetMode, policy, ...(targetMode === "tui" && settings?.model ? { model: settings.model } : {}), ...(targetMode === "tui" && settings?.reasoningEffort ? { reasoningEffort: settings.reasoningEffort } : {}) };
			const result = await source.startInterfaceTransition(sessionId, input, { signal: controller.signal });
			if (!owns() || controller.signal.aborted) return;
			const next = { ...statusRef.current, supported: statusRef.current?.supported ?? true, targetMode, currentMode: result.transition.sourceMode, transition: result.transition };
			statusRef.current = next; setOwned({ key: ownerKey, status: next }); setFailure({ count: 0 });
		} catch (cause) { if (owns() && !controller.signal.aborted) setError(cloudRequestErrorCopy(cause)); throw cause; }
		finally { requests.current.delete(controller); if (owns() && !controller.signal.aborted) { busy.current = false; setStarting(false); setStartingPolicy(undefined); } }
	}, [controllerFor, ownerKey, owns, sessionId, source]);
	const cancel = useCallback(async () => {
		if (!owns() || !source?.cancelInterfaceTransition) throw new Error("Cloud cancellation is unavailable.");
		if (!mobileInterfaceTransitionIsCancellable(statusRef.current?.transition)) throw new Error("This switch can no longer be cancelled.");
		setCancelling(true); const controller = controllerFor();
		try { await source.cancelInterfaceTransition(sessionId, { signal: controller.signal }); if (owns() && !controller.signal.aborted) await refresh(); }
		catch (cause) { if (owns() && !controller.signal.aborted) setError(cloudRequestErrorCopy(cause)); throw cause; }
		finally { requests.current.delete(controller); if (owns()) setCancelling(false); }
	}, [controllerFor, owns, refresh, sessionId, source]);
	const acknowledgeNotice = useCallback(async (transitionId: string) => {
		if (!owns() || !source?.acknowledgeInterfaceNotice) throw new Error("Cloud notice dismissal is unavailable.");
		setAcknowledgingNotice(true); setAcknowledgeNoticeError(undefined); const controller = controllerFor();
		try { await source.acknowledgeInterfaceNotice(sessionId, transitionId, { signal: controller.signal }); if (owns() && !controller.signal.aborted) await refresh(); }
		catch (cause) { if (owns() && !controller.signal.aborted) setAcknowledgeNoticeError(cloudRequestErrorCopy(cause)); throw cause; }
		finally { requests.current.delete(controller); if (owns()) setAcknowledgingNotice(false); }
	}, [controllerFor, owns, refresh, sessionId, source]);
	return { status, transition: status?.transition, loading, starting, startingPolicy, cancelling, acknowledgingNotice, acknowledgeNoticeError, error, fetchFailed: failure.count > 0, start, cancel, acknowledgeNotice, refresh };
}
export type CloudInterfaceControls = ReturnType<typeof useCloudInterfaceTransition>;
