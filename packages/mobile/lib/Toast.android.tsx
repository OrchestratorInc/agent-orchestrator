import { Host, SnackbarHost, type SnackbarHostRef } from "@expo/ui/jetpack-compose";
import { useEffect, useRef, useState } from "react";
import { StyleSheet, View } from "react-native";
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
 * while no message is up. While one is up (or queued) the snackbar takes the
 * touches over itself, as a native one does: a dismissible one's close button
 * needs them, and so does TalkBack's explore-by-touch, to find any message.
 * Compose queues a message shown while another is up and cannot take one down
 * early, so `hide` has no effect on a snackbar already showing.
 */
export function ToastHost({ toast, bottomOffset }: ToastHostProps) {
	const { scheme } = useThemeState();
	const insets = useSafeAreaInsets();
	const snackbar = useRef<SnackbarHostRef>(null);
	// Snackbars that Compose has not finished with, on screen or queued.
	const [pending, setPending] = useState(0);
	const { current, hide } = toast;

	useEffect(() => {
		if (!current) return;
		const shown = snackbar.current?.showSnackbar({ message: current.text, duration: current.duration, withDismissAction: current.dismissible });
		if (!shown) return;
		setPending((count) => count + 1);
		const done = () => {
			hide(current.id);
			setPending((count) => count - 1);
		};
		shown.then(done, done);
	}, [current, hide]);

	return (
		// The Compose host ignores `pointerEvents`, so this view decides whether
		// touches, and the hover events explore-by-touch rides on, reach the
		// snackbar. Not collapsable: flattening it away when it turns touchable
		// would move the host, and the snackbar with it.
		<View
			collapsable={false}
			pointerEvents={pending > 0 ? "box-none" : "none"}
			style={[styles.anchor, { bottom: bottomOffset ?? insets.bottom + space.xl }]}
		>
			<Host
				matchContents={{ vertical: true }}
				// The screen places the snackbar, so it does not also lift itself over
				// the keyboard: the same offset holds on both platforms.
				ignoreSafeAreaKeyboardInsets
				colorScheme={scheme}
			>
				<SnackbarHost ref={snackbar} />
			</Host>
		</View>
	);
}

const styles = StyleSheet.create({
	anchor: { position: "absolute", left: gutter, right: gutter },
});
