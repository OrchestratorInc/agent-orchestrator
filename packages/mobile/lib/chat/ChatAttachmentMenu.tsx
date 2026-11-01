import { MenuView, type MenuAction, type MenuComponentRef, type NativeActionEvent } from "@expo/ui/community/menu";
import { useRef } from "react";
import { Platform, Pressable, StyleSheet, View } from "react-native";
import { AppSymbol } from "../AppSymbol";
import { usePressFeedback } from "../pressFeedback";
import { useTheme, useThemeState } from "../ThemeProvider";
import { iconSize, radius, stateOpacity, touchTarget } from "../tokens";
import { chatMenuIcon } from "./chatMenuIcons";
import { COMPOSER_DISC } from "./composerStyles";

/**
 * The composer's way to attach a photo or a file: the system's pull-down menu,
 * SwiftUI's on iOS and Compose's dropdown on Android.
 */
export function ChatAttachmentMenu({
	disabled,
	canAttachFile,
	onChoosePhoto,
	onChooseFile,
}: {
	disabled: boolean;
	canAttachFile: boolean;
	onChoosePhoto(): void;
	onChooseFile(): void;
}) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const feedback = usePressFeedback("icon");
	const menu = useRef<MenuComponentRef>(null);
	const actions: MenuAction[] = [
		{ id: "photo", title: "Choose Photo", image: chatMenuIcon("photo") },
		...(canAttachFile ? [{ id: "file", title: "Choose File", image: chatMenuIcon("file") } satisfies MenuAction] : []),
	];
	const button = { accessibilityRole: "button", accessibilityLabel: "Attach", accessibilityState: { disabled } } as const;
	// A plus, not a paperclip: the composer is one row of three controls, and the
	// clip read as an attachment badge on the field rather than a way in.
	// The plus in a filled circle, the height of the model's capsule beside it,
	// inside a full touch target.
	const glyph = <View style={[styles.disc, { backgroundColor: t.bgElevated }]}><AppSymbol name="add" size={iconSize.lg} color={t.textSecondary} /></View>;

	if (disabled) return <View accessible {...button} style={[styles.trigger, styles.disabled]}>{glyph}</View>;

	// No haptic: choosing opens the system picker, a move to another screen.
	const choose = (event: NativeActionEvent) => {
		if (event.nativeEvent.event === "photo") onChoosePhoto();
		if (event.nativeEvent.event === "file") onChooseFile();
	};

	return (
		<MenuView ref={menu} title="Attach" colorScheme={scheme} actions={actions} onPressAction={choose} testID="chat-attachment-menu">
			{Platform.OS === "android" ? (
				// Compose's trigger is no accessibility node and draws no ripple, so
				// Android's is a button of its own that opens the menu.
				<Pressable {...button} android_ripple={feedback.android_ripple} onPress={() => menu.current?.show()} style={styles.trigger}>
					{glyph}
				</Pressable>
			) : (
				// SwiftUI's Menu answers the tap on its label, so iOS's stays a plain view.
				<View accessible {...button} style={styles.trigger}>{glyph}</View>
			)}
		</MenuView>
	);
}

const styles = StyleSheet.create({
	trigger: { width: touchTarget, height: touchTarget, alignItems: "center", justifyContent: "center" },
	disc: { width: COMPOSER_DISC, height: COMPOSER_DISC, borderRadius: radius.pill, alignItems: "center", justifyContent: "center" },
	disabled: { opacity: stateOpacity.disabled },
});
