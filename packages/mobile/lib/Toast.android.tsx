import { Host, SnackbarHost, type SnackbarHostRef } from "@expo/ui/jetpack-compose";
import { useEffect, useRef } from "react";
import { StyleSheet } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useThemeState } from "./ThemeProvider";
import type { ToastHostProps } from "./Toast.types";
import { gutter, space } from "./tokens";

export type { ToastController, ToastDuration, ToastHostProps, ToastMessage, ToastOptions, ToastTone } from "./Toast.types";

/**
 * The Android transient message: Material 3's own Snackbar, in the system's
 * colours, with Compose's timing, motion and TalkBack announcement.
 *
 * The host stays mounted and takes the snackbar's height, so it covers nothing
 * while no message is up, and lets touches through while one is: a message
 * carries no action here, so there is nothing on it to tap. Compose queues a
 * message shown while another is up and cannot take one down early, so `hide`
 * has no effect on a snackbar already showing.
 */
export function ToastHost({ toast, bottomOffset }: ToastHostProps) {
	const { scheme } = useThemeState();
	const insets = useSafeAreaInsets();
	const snackbar = useRef<SnackbarHostRef>(null);
	const { current, hide } = toast;

	useEffect(() => {
		if (!current) return;
		const done = () => hide(current.id);
		snackbar.current?.showSnackbar({ message: current.text, duration: current.duration }).then(done, done);
	}, [current, hide]);

	return (
		<Host
			matchContents={{ vertical: true }}
			// The screen places the snackbar, so it does not also lift itself over
			// the keyboard: the same offset holds on both platforms.
			ignoreSafeAreaKeyboardInsets
			colorScheme={scheme}
			pointerEvents="none"
			style={[styles.anchor, { bottom: bottomOffset ?? insets.bottom + space.xl }]}
		>
			<SnackbarHost ref={snackbar} />
		</Host>
	);
}

const styles = StyleSheet.create({
	anchor: { position: "absolute", left: gutter, right: gutter },
});
