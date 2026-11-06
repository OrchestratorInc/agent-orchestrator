import { Alert, Platform } from "react-native";
import { describePrompt } from "./storeUpdate";

/** The store a native update comes from, named as the platform names it. */
export function storeName(os: string = Platform.OS): string {
	return os === "ios" ? "App Store" : "Play Store";
}

export type StoreUpdatePrompt = {
	/** The version to name, when there is one worth naming. */
	version?: string;
	/** Whether the store itself said so, rather than only the version floor. */
	storeConfirmed: boolean;
	onUpdate(): void;
	/** "Not now", and on Android also Back or a tap outside the dialog. */
	onDismiss?(): void;
};

/**
 * The one prompt for a newer native binary, from the launch check and from
 * Settings alike: the system alert, which Android draws as its AlertDialog.
 * OTA updates never come through here; they install on their own.
 */
export function promptStoreUpdate({ version, storeConfirmed, onUpdate, onDismiss }: StoreUpdatePrompt): void {
	const store = storeName();
	Alert.alert(
		"A newer version of Orchestrator is ready",
		`${describePrompt({ version, storeConfirmed, storeName: store })} Update the native app for the latest compatibility, fixes, and system integrations.`,
		[
			{ text: "Not now", style: "cancel", onPress: onDismiss },
			{ text: `Open ${store}`, isPreferred: true, onPress: onUpdate },
		],
		// Android only: Back and a tap outside close the dialog without a button,
		// and that is a "Not now" too, or the snooze would never be written.
		{ cancelable: true, onDismiss },
	);
}
