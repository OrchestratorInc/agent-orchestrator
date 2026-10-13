import type { Session, SessionInterfaceTransitionStatus } from "@aoagents/cloud-client";
import { mobileInterfaceTransitionIsActive } from "../session/interfaceTransition";

export type CloudInterfaceStatus = SessionInterfaceTransitionStatus & { currentMode?: "chat" | "tui"; runtimeConnected?: boolean };
export function mergeCloudInterfaceStatus(previous: SessionInterfaceTransitionStatus | undefined, wire: SessionInterfaceTransitionStatus, session: Session): CloudInterfaceStatus {
	let transition = wire.transition;
	const old = previous?.transition;
	if (!transition && old) {
		if ((mobileInterfaceTransitionIsActive(old) || old.phase === "completed" || old.phase === "recovery_required") && session.interfaceMode === old.targetMode && session.runtimeConnected && !session.isTerminated) transition = { ...old, phase: "completed" };
		else if ((mobileInterfaceTransitionIsActive(old) || old.phase === "recovery_required") && session.interfaceMode === old.sourceMode && session.runtimeConnected && !session.isTerminated) transition = { ...old, phase: "cancelled" };
		else if (old.phase === "cancelled" || old.phase === "failed" || old.phase === "recovery_required") transition = old;
		else if (mobileInterfaceTransitionIsActive(old)) transition = old;
	}
	return { ...wire, transition, currentMode: session.interfaceMode, runtimeConnected: session.runtimeConnected && !session.isTerminated };
}
export function cloudInterfaceInputLocked(status?: CloudInterfaceStatus): boolean {
	return mobileInterfaceTransitionIsActive(status?.transition) || status?.transition?.phase === "recovery_required";
}
export function cloudInterfaceSettledTarget(status?: CloudInterfaceStatus): "chat" | "tui" | undefined {
	const transition = status?.transition;
	return transition?.phase === "completed" && status?.currentMode === transition.targetMode && status.runtimeConnected ? transition.targetMode : undefined;
}
export function cloudInterfaceDisplayMode(status: CloudInterfaceStatus | undefined, fallback: "chat" | "tui"): "chat" | "tui" {
	return cloudInterfaceInputLocked(status) ? status?.transition?.sourceMode ?? fallback : status?.currentMode ?? fallback;
}
export function cloudInterfacePollDelay(input: { status?: CloudInterfaceStatus; failures?: number; httpStatus?: number; retryAfterSeconds?: number; readinessAttempts?: number }): number | undefined {
	if ([401, 403, 404, 410].includes(input.httpStatus ?? 0)) return undefined;
	if (input.httpStatus === 429) return Math.max(1000, (input.retryAfterSeconds ?? 30) * 1000);
	const failures = input.failures ?? 0;
	if (failures) return !mobileInterfaceTransitionIsActive(input.status?.transition) && failures >= 5 ? undefined : Math.min(8000, 1000 * 2 ** Math.min(3, failures - 1));
	if (input.status?.transition?.phase === "recovery_required") return undefined;
	if (mobileInterfaceTransitionIsActive(input.status?.transition)) return 1000;
	if (["NATIVE_SESSION_MISSING", "NATIVE_SESSION_UNVERIFIED"].includes(input.status?.reasonCode ?? "") && (input.readinessAttempts ?? 0) < 10) return 1000;
	return undefined;
}
export async function cloudInterfaceDelay(ms: number, signal?: AbortSignal): Promise<void> {
	signal?.throwIfAborted();
	await new Promise<void>((resolve) => {
		const finish = () => { clearTimeout(timer); signal?.removeEventListener("abort", finish); resolve(); };
		const timer = setTimeout(finish, ms); signal?.addEventListener("abort", finish, { once: true });
		if (signal?.aborted) finish();
	});
	signal?.throwIfAborted();
}
