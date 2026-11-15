import AsyncStorage from "@react-native-async-storage/async-storage";
import { removeHost, saveHost } from "../hosts";
import { DEMO_HOST_ID, demoEndpoint } from "./server";
import { isDevBuild, setDemoNetwork, type DemoNetwork } from "./transport";

// Pairing and settings for the in-app demo machine, without React, so they can
// be tested on their own. See ./index.ts.

export const NETWORK_KEY = "ao.dev.demoNetwork";
export const DEMO_MACHINE_NAME = "Demo machine";

export async function addDemoMachine(): Promise<void> {
	if (!isDevBuild()) return;
	await saveHost({
		id: DEMO_HOST_ID,
		name: DEMO_MACHINE_NAME,
		appearance: { symbol: "laptop" },
		platform: "darwin",
		endpoints: [demoEndpoint()],
		token: "demo-token",
		lastConnected: Date.now(),
	});
}

export async function removeDemoMachine(): Promise<void> {
	await removeHost(DEMO_HOST_ID);
}

export function chooseDemoNetwork(next: DemoNetwork): void {
	setDemoNetwork(next);
	void AsyncStorage.setItem(NETWORK_KEY, next).catch(() => {});
}
