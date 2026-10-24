import { Feather } from "../icons";
import { MenuView, type MenuAction, type NativeActionEvent } from "@expo/ui/community/menu";
import { useRef } from "react";
import { StyleSheet, View } from "react-native";
import { haptics } from "../haptics";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { iconSize, type } from "../tokens";

export function ChatAttachmentMenu({
	disabled,
	canAttachPhoto,
	canAttachFile,
	onChoosePhoto,
	onChooseFile,
}: {
	disabled: boolean;
	canAttachPhoto: boolean;
	canAttachFile: boolean;
	onChoosePhoto(): void;
	onChooseFile(): void;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const permissions = useRef({ disabled, canAttachPhoto, canAttachFile, onChoosePhoto, onChooseFile });
	permissions.current = { disabled, canAttachPhoto, canAttachFile, onChoosePhoto, onChooseFile };
	if (!canAttachPhoto && !canAttachFile) return null;
	const actions: MenuAction[] = [
		...(canAttachPhoto ? [{ id: "photo", title: "Choose Photo", image: "photo" } satisfies MenuAction] : []),
		...(canAttachFile ? [{ id: "file", title: "Choose File", image: "doc" } satisfies MenuAction] : []),
	];
	const trigger = (
		<View
			accessible
			accessibilityRole="button"
			accessibilityLabel="Attach"
			accessibilityState={{ disabled }}
			style={[styles.trigger, disabled && styles.disabled]}
		>
			{/* A plus, not a paperclip: the composer is one row of three controls, and
			    the clip read as an attachment badge on the field rather than a way in. */}
			<Feather name="plus" size={iconSize.xl} color={disabled ? t.textFaint : t.textSecondary} />
		</View>
	);

	if (disabled) return trigger;

	const choose = (event: NativeActionEvent) => {
		const current = permissions.current;
		if (current.disabled) return;
		haptics.tap();
		if (event.nativeEvent.event === "photo" && current.canAttachPhoto) current.onChoosePhoto();
		if (event.nativeEvent.event === "file" && current.canAttachFile) current.onChooseFile();
	};

	return (
		<MenuView title="Attach" actions={actions} onPressAction={choose} testID="chat-attachment-menu">
			{trigger}
		</MenuView>
	);
}

const makeStyles = (_t: Theme) => StyleSheet.create({
	trigger: { width: 44, height: 44, borderRadius: 22, borderCurve: "continuous", alignItems: "center", justifyContent: "center" },
	disabled: { opacity: 0.55 },
});
