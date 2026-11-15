export type NotificationType = "needs_input" | "ready_to_merge" | "pr_merged" | "pr_closed_unmerged";

const ATTENTION_TYPES: ReadonlySet<string> = new Set<NotificationType>(["needs_input", "ready_to_merge"]);

export function shouldSignalAttention(type: string | undefined): boolean {
	return type !== undefined && ATTENTION_TYPES.has(type);
}

export function shouldReplaceBounce(pending: { critical: boolean } | null): boolean {
	return pending === null || !pending.critical;
}

export function shouldToast(notification: { title?: string }, isSupported: boolean): boolean {
	return Boolean(notification.title) && isSupported;
}

export function osToastChimeControllable(platform: NodeJS.Platform): boolean {
	return platform === "darwin" || platform === "win32";
}

export function toastSilent(
	platform: NodeJS.Platform,
	soundNotificationsEnabled: boolean,
	playsSound: boolean,
): boolean | undefined {
	if (!osToastChimeControllable(platform)) return undefined;
	return !soundNotificationsEnabled || playsSound;
}

export function dockBounceType(type: string | undefined): "critical" | "informational" {
	return type === "needs_input" ? "critical" : "informational";
}
