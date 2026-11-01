import { Host } from "@expo/ui";
import { List, Text, VStack } from "@expo/ui/swift-ui";
import {
	accessibilityElement,
	accessibilityLabel,
	deleteDisabled,
	font,
	foregroundStyle,
	lineLimit,
	listRowBackground,
	listStyle,
	scrollContentBackground,
	truncationMode,
} from "@expo/ui/swift-ui/modifiers";
import { StyleSheet } from "react-native";
import { useTheme, useThemeState } from "../ThemeProvider";
import { queuedCommentLabel, type QueuedCommentsProps } from "./QueuedComments.types";

export type { QueuedCommentsProps } from "./QueuedComments.types";

/**
 * The queued comments as a SwiftUI list: swipe a row to delete it, as in Mail,
 * with the full swipe and VoiceOver's Delete action that come with it.
 * Android's is ./QueuedComments.android.
 */
export function QueuedComments({ comments, onRemove, disabled = false }: QueuedCommentsProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	return (
		<Host colorScheme={scheme} style={styles.fill}>
			<List modifiers={[listStyle("plain"), scrollContentBackground("hidden")]}>
				<List.ForEach
					onDelete={(indices) => {
						// Read the ids before removing any: each removal shifts the rest.
						for (const id of indices.map((index) => comments[index]?.id)) if (id) onRemove(id);
					}}
				>
					{comments.map((comment) => (
						<VStack
							key={comment.id}
							alignment="leading"
							spacing={4}
							modifiers={[
								listRowBackground(t.bgSurface),
								deleteDisabled(disabled),
								accessibilityElement("combine"),
								accessibilityLabel(queuedCommentLabel(comment)),
							]}
						>
							<Text modifiers={[font({ textStyle: "footnote", design: "monospaced" }), foregroundStyle(t.textTertiary), lineLimit(1), truncationMode("middle")]}>{comment.label}</Text>
							<Text modifiers={[font({ textStyle: "subheadline" }), foregroundStyle(t.textPrimary)]}>{comment.feedback}</Text>
						</VStack>
					))}
				</List.ForEach>
			</List>
		</Host>
	);
}

const styles = StyleSheet.create({
	fill: { flex: 1 },
});
