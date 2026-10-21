import AsyncStorage from "@react-native-async-storage/async-storage";

// Storage side of `localNetworkWait.ts`, split out so that module stays free of
// React Native imports and unit-testable. Not a secret, so AsyncStorage.

const LOCAL_NETWORK_CONFIRMED_KEY = "ao.localNetworkConfirmed";

/** Whether a LAN request has ever gone through. Never throws — storage failure reads as "not yet". */
export async function loadLocalNetworkConfirmed(): Promise<boolean> {
	try {
		return (await AsyncStorage.getItem(LOCAL_NETWORK_CONFIRMED_KEY)) === "1";
	} catch {
		return false;
	}
}

export async function setLocalNetworkConfirmed(): Promise<void> {
	try {
		await AsyncStorage.setItem(LOCAL_NETWORK_CONFIRMED_KEY, "1");
	} catch {
		/* a lost flag only means the next unreachable LAN scan waits before saying so */
	}
}
