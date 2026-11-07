import { useState } from "react";
import { Platform } from "react-native";
import { ApiError, pingServer } from "./api";
import { classifyConnectionFailure, describeConnectionFailure } from "./connectionError";
import { haptics } from "./haptics";
import { useApp } from "./store";
import type { ToastController } from "./Toast.types";

/**
 * Tests a machine's connection on demand. The page's status stays the live
 * one; what the test found is said in a toast, so the state is never shown
 * twice.
 */
export function useMachineConnectionTest(notify: ToastController["show"]) {
	const { config, configForHost, reloadConfig, refresh, refreshHost } = useApp();
	const [testing, setTesting] = useState(false);

	async function test({ hostId, name, isDefault }: { hostId: string; name: string; isDefault: boolean }) {
		setTesting(true);
		let target = isDefault ? config : configForHost(hostId);
		let rejected = false;
		try {
			if (isDefault) {
				// Race every known path first, like the app itself does, rather than
				// pinging only the last address that happened to win. Without the
				// endpoint refresh: it is authenticated, so with a stale password it
				// and the ping would spend two failed attempts per tap towards the
				// lockout.
				target = await reloadConfig({ refreshEndpoints: false });
			} else if (!target) {
				// No verified route to this machine yet: its own runner races its
				// routes first, as its poll would.
				await refreshHost(hostId);
				target = configForHost(hostId);
			}
			if (!target) throw new Error("No verified connection to this machine");
			await pingServer(target);
			haptics.success();
			notify(`${name} is connected.`, { tone: "success" });
		} catch (error) {
			haptics.error();
			const status = error instanceof ApiError ? error.status : undefined;
			const failure = classifyConnectionFailure(status);
			rejected = failure === "auth" || failure === "rate-limited";
			const { title } = describeConnectionFailure(failure, {
				host: target?.host ?? "",
				port: target?.httpPort ?? "",
				platform: Platform.OS,
			});
			notify(`${name}: ${title}`, { tone: "warning", duration: "long" });
		} finally {
			// Poll now so the status lands on the same answer instead of waiting
			// out the poll interval — unless the desktop rejected the password:
			// another request would spend a second failed attempt towards its
			// lockout, and the poll already reports a rejection on its own. Not
			// awaited: against a dead address it is another full request timeout.
			if (!rejected) void (isDefault ? refresh() : refreshHost(hostId));
			setTesting(false);
		}
	}

	return { testing, test };
}
