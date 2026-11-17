import AsyncStorage from "@react-native-async-storage/async-storage";
import { useEffect, useSyncExternalStore } from "react";
import { loadHosts } from "../hosts";
import { useApp } from "../store";
import { addDemoMachine, NETWORK_KEY } from "./machine";
import { DEMO_HOST_ID } from "./server";
import { DEMO_NETWORKS, demoNetwork, isDevBuild, setDemoNetwork, subscribeDemoNetwork, type DemoNetwork } from "./transport";

// Development builds only: a paired "Demo machine" that lives inside the app.
// Turning it on saves a machine whose address only ./transport.ts answers, so
// the whole app (pairing records, the connect race, polls, the chat stream)
// runs exactly as against a real machine, with no network and no daemon.
// Start Metro with EXPO_PUBLIC_AO_DEMO=1 to add it on launch, or use the
// switch in Settings → Demo machine.

export { addDemoMachine, chooseDemoNetwork, DEMO_MACHINE_NAME, removeDemoMachine } from "./machine";
export { DEMO_HOST_ID } from "./server";
export { DEMO_NETWORKS, installDemoTransport, type DemoNetwork } from "./transport";

export function useDemoNetwork(): DemoNetwork {
	return useSyncExternalStore(subscribeDemoNetwork, demoNetwork, demoNetwork);
}

/**
 * Headless, mounted inside AppProvider. Restores the chosen network and, when
 * Metro was started with EXPO_PUBLIC_AO_DEMO=1, adds the demo machine and
 * switches to it on launch.
 */
export function DemoMachineManager() {
	const { switchHost, reloadHostMetadata } = useApp();
	useEffect(() => {
		if (!isDevBuild()) return;
		let cancelled = false;
		void (async () => {
			const stored = await AsyncStorage.getItem(NETWORK_KEY).catch(() => null);
			if (stored && stored in DEMO_NETWORKS) setDemoNetwork(stored as DemoNetwork);
			if (process.env.EXPO_PUBLIC_AO_DEMO !== "1") return;
			const hosts = await loadHosts();
			if (cancelled || hosts.some((host) => host.id === DEMO_HOST_ID)) return;
			await addDemoMachine();
			await reloadHostMetadata();
			await switchHost(DEMO_HOST_ID);
		})().catch((error) => console.warn("[demo] couldn't add the demo machine", error));
		return () => { cancelled = true; };
		// Once per launch.
	}, []);
	return null;
}
