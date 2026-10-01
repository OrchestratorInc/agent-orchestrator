import { notificationTarget } from "./notificationView";

export function shouldUnpairPush(previousLocalConfigured: boolean, localConfigured: boolean): boolean {
	return previousLocalConfigured && !localConfigured;
}

export function pushNotificationTarget(localConfigured: boolean, data: Parameters<typeof notificationTarget>[0]) {
	if (!localConfigured) return undefined;
	const target = notificationTarget(data);
	// Legacy daemon pushes have no host identity. A session ID may belong to a
	// previous pairing, so let the person choose from the current Workers board.
	return target.startsWith("/session/") ? "/" : target;
}
