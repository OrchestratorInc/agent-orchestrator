import { Stack } from "expo-router";
import type { ReactElement } from "react";
import { Platform, StyleSheet, View } from "react-native";
import { Spinner } from "../Spinner";
import type { Theme } from "../theme";
import { toolbarIcon, type ToolbarSymbol } from "../toolbarIcon";
import { touchTarget } from "../tokens";

/** The preview globe once there is something to open: filled, a shape change and not only a colour. */
export const PREVIEW_READY_GLYPH: ToolbarSymbol = { ios: "globe.americas.fill", android: "language" };

export type SessionHeaderButtonsOptions = {
	/** The other interface, which the first button switches to: Chat from the terminal, Terminal UI from Chat. */
	switchTo: "chat" | "terminal";
	/** The daemon says the other interface can resume this session. */
	switchReady: boolean;
	/** A check or a switch is in flight: the button gives way to a spinner. */
	switchBusy: boolean;
	/** What the spinner says while it shows. */
	switchBusyLabel: string;
	/** A switch is already under way, so another cannot start. */
	switchDisabled: boolean;
	/** Something the agent produced is ready to preview (see `useSessionPreview`). */
	previewReady: boolean;
	onSwitch(): void;
	onPreview(): void;
	onFiles(): void;
	t: Theme;
};

/**
 * The buttons a session's header leads with, the same in Chat and the terminal:
 * the switch to the other interface, the preview and the session's files, in
 * that order, each the platform's own bar button. Each screen adds its "⋯" after
 * them.
 *
 * A function rather than a component: Stack.Toolbar reads its children by
 * element type, so it has to be handed the Stack.Toolbar items themselves.
 */
export function sessionHeaderButtons({ switchTo, switchReady, switchBusy, switchBusyLabel, switchDisabled, previewReady, onSwitch, onPreview, onFiles, t }: SessionHeaderButtonsOptions): ReactElement[] {
	const ios = Platform.OS === "ios";
	return [
		// A spinner rather than a dimmed glyph: a recheck only runs while the glyph
		// is already faint, so fading it further reads as broken, and a starting
		// switch is otherwise a silent, disabled button for up to a request timeout.
		switchBusy ? (
			<Stack.Toolbar.View key="switch" hidesSharedBackground>
				<View style={styles.spinner}>
					<Spinner color={t.accent} accessibilityLabel={switchBusyLabel} />
				</View>
			</Stack.Toolbar.View>
		) : (
			<Stack.Toolbar.Button
				key="switch"
				icon={toolbarIcon(switchTo)}
				accessibilityLabel={switchTo === "chat" ? "Open Chat interface" : "Open Terminal UI"}
				disabled={switchDisabled}
				// Faint until the daemon says the switch can resume this session. It
				// stays tappable: the tap asks again and explains what is missing.
				tintColor={switchReady ? undefined : t.textFaint}
				onPress={onSwitch}
			/>
		),
		<Stack.Toolbar.Button
			key="preview"
			// iOS draws a bar-button badge only from iOS 26, and an empty one is a
			// blank capsule there, so on iOS the globe itself changes: it fills in,
			// and turns green as well.
			icon={toolbarIcon(previewReady && ios ? PREVIEW_READY_GLYPH : "globe")}
			accessibilityLabel={previewReady ? "Open preview, preview ready" : "Open preview"}
			tintColor={previewReady && ios ? t.green : undefined}
			onPress={onPreview}
		>
			{previewReady && !ios ? <Stack.Toolbar.Badge style={{ backgroundColor: t.green }} /> : null}
		</Stack.Toolbar.Button>,
		<Stack.Toolbar.Button
			key="files"
			icon={toolbarIcon("fileSearch")}
			accessibilityLabel="Session files and changes"
			onPress={onFiles}
		/>,
	];
}

const styles = StyleSheet.create({
	spinner: { width: touchTarget, height: touchTarget, alignItems: "center", justifyContent: "center" },
});
