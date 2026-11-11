import * as Device from "expo-device";
import { Platform } from "react-native";
import { getInstallId } from "../installId";
import { expoPushToken } from "../push";
import type { CloudClient } from "./client";
import { CloudError } from "./errors";

export type CloudPushResult = { ok: true } | { ok: false; reason: "unavailable" | "unsupported-server" | "failed" };

/**
 * Hand this phone's Expo push token to Orchestrator Cloud, so the control plane can
 * deliver its notifications (needs input, PR opened, finished) while the app
 * is closed. Keyed by install id, so a re-registration replaces the token in
 * place. A control plane that predates push devices answers 404; that is not
 * an error the person can act on, so it is reported and otherwise ignored.
 */
export async function registerCloudPush(client: Pick<CloudClient, "putPushDevice">, { ask }: { ask: boolean }): Promise<CloudPushResult> {
	const token = await expoPushToken({ ask });
	if (!token) return { ok: false, reason: "unavailable" };
	try {
		await client.putPushDevice(await getInstallId(), { token, platform: Platform.OS, deviceName: Device.deviceName ?? undefined });
		return { ok: true };
	} catch (error) {
		if (error instanceof CloudError && (error.status === 404 || error.status === 405)) return { ok: false, reason: "unsupported-server" };
		return { ok: false, reason: "failed" };
	}
}

/**
 * Stop Orchestrator Cloud pushing to this phone. Runs before sign-out, while
 * the token still works, and reports whether it did: the Expo token stays
 * valid after sign-out, so a removal that failed leaves the account's
 * notifications coming until the phone signs in again (which moves the token
 * to that account). A control plane without push devices has nothing to remove.
 */
export async function unregisterCloudPush(client: Pick<CloudClient, "deletePushDevice">): Promise<boolean> {
	try {
		await client.deletePushDevice(await getInstallId());
		return true;
	} catch (error) {
		return error instanceof CloudError && (error.status === 404 || error.status === 405);
	}
}
