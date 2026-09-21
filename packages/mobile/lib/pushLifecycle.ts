import type { EnvironmentKind } from "./environment/types";
import { notificationTarget } from "./notificationView";

export function shouldUnpairPush(previousLocalConfigured: boolean, localConfigured: boolean): boolean {
	return previousLocalConfigured && !localConfigured;
}

export function pushNotificationTarget(environment: EnvironmentKind | null, data: Parameters<typeof notificationTarget>[0]) {
	if (environment !== "local") return undefined;
	return notificationTarget(data);
}
