import { Host } from "@expo/ui";
import { Button, ConfirmationDialog, Spacer, Text } from "@expo/ui/swift-ui";
import { frame } from "@expo/ui/swift-ui/modifiers";
import { StyleSheet } from "react-native";
import { useThemeState } from "../ThemeProvider";
import { space } from "../tokens";
import { INTERFACE_SWITCH_CONFIRM as copy, type InterfaceSwitchConfirmProps } from "./InterfaceSwitchConfirm.types";

export type { InterfaceSwitchConfirmProps, InterfaceSwitchPolicy } from "./InterfaceSwitchConfirm.types";

/**
 * Asks how a busy session should switch to Chat, as iOS asks a choice between
 * actions: a confirmation dialog, which is an action sheet on iPhone and a
 * popover on iPad. Android keeps its three-button dialog
 * (./InterfaceSwitchConfirm.android).
 *
 * SwiftUI presents the dialog from a view, so this renders a one-point anchor.
 * The screen places it under the header's Chat item, where the iPad popover
 * should point.
 */
export function InterfaceSwitchConfirm({ message, onChoose, onCancel }: InterfaceSwitchConfirmProps) {
	const { scheme } = useThemeState();
	return (
		<Host style={styles.anchor} colorScheme={scheme}>
			<ConfirmationDialog
				title={copy.title}
				titleVisibility="visible"
				isPresented={message !== null}
				// Closing by any route lands here, an answer included; the screen's
				// handlers only clear the question, so the second call is harmless.
				onIsPresentedChange={(presented) => {
					if (!presented) onCancel();
				}}
			>
				<ConfirmationDialog.Trigger>
					<Spacer modifiers={[frame({ width: 1, height: 1 })]} />
				</ConfirmationDialog.Trigger>
				<ConfirmationDialog.Actions>
					<Button label={copy.drain} onPress={() => onChoose("drain")} />
					<Button label={copy.interrupt} role="destructive" onPress={() => onChoose("interrupt")} />
					<Button label={copy.cancel} role="cancel" onPress={onCancel} />
				</ConfirmationDialog.Actions>
				<ConfirmationDialog.Message>
					<Text>{message ?? ""}</Text>
				</ConfirmationDialog.Message>
			</ConfirmationDialog>
		</Host>
	);
}

const styles = StyleSheet.create({
	anchor: { position: "absolute", top: 0, right: space.lg, width: 1, height: 1 },
});
