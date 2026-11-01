import { Feather } from "../icons";
import { FlatList, Pressable, StyleSheet, Text, View } from "react-native";
import { AppSymbol } from "../AppSymbol";
import { rowDividerWidth } from "../divider";
import { usePressFeedback } from "../pressFeedback";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { EmptyState, SheetHeader } from "../ui";
import { conversationMarkerPresentation } from "./chatPresentation";
import type { ConversationMarker } from "./timelineModel";
import { iconSize, space, type } from "../tokens";

export function ConversationMapSheet({ markers, onSelect }: { markers: ConversationMarker[]; onSelect(sequence: number): void }) {
	const styles = useThemedStyles(makeStyles);
	return <FlatList
		style={styles.list}
		contentContainerStyle={styles.content}
		data={markers}
		keyExtractor={(item) => item.key}
		// Keeps an Android drag with the list; without it the sheet's own pan
		// takes the gesture and dismisses instead of scrolling back up.
		nestedScrollEnabled
		ListHeaderComponent={<>
			<View style={styles.header}><SheetHeader title="Conversation history" subtitle={markers.length ? `${markers.length} ${markers.length === 1 ? "exchange" : "exchanges"}` : "Jump between the important moments in this conversation."} /></View>
			{markers.length ? <View style={styles.sectionHeader}><Text accessibilityRole="header" style={styles.sectionTitle}>History</Text><View style={styles.sectionRule} /></View> : null}
		</>}
		ListEmptyComponent={<EmptyState icon="chat" title="No exchanges yet" message="Messages and completed work will appear here as the conversation grows." />}
		renderItem={({ item }) => <MarkerRow marker={item} onSelect={onSelect} />}
	/>;
}

// Jumping is moving through the conversation, not a moment worth a haptic.
function MarkerRow({ marker, onSelect }: { marker: ConversationMarker; onSelect(sequence: number): void }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const feedback = usePressFeedback("row");
	const presentation = conversationMarkerPresentation(marker.state);
	const color = presentation.tone === "working" ? t.orange : presentation.tone === "success" ? t.green : presentation.tone === "danger" ? t.red : presentation.tone === "accent" ? t.accent : t.textTertiary;
	return <Pressable
		accessibilityRole="button"
		accessibilityLabel={`Jump to ${marker.title}, ${presentation.label}`}
		android_ripple={feedback.android_ripple}
		onPress={() => onSelect(marker.sequence)}
		style={({ pressed }) => [styles.row, pressed && feedback.pressed]}
	>
		<View style={styles.eyebrow}>
			<Feather name={presentation.icon} size={iconSize.xs} color={color} />
			<Text style={[styles.state, { color }]}>{presentation.label}</Text>
			<View style={styles.spacer} />
			<AppSymbol name="chevronForward" size={iconSize.sm} color={t.textFaint} />
		</View>
		<Text numberOfLines={2} style={styles.title}>{marker.title}</Text>
		{marker.detail ? <Text numberOfLines={2} style={styles.detail}>{marker.detail}</Text> : null}
	</Pressable>;
}

const makeStyles = (t: Theme) => StyleSheet.create({
	list: { flex: 1, backgroundColor: t.bgBase },
	// Grows to the sheet so an empty history centres its message.
	content: { flexGrow: 1, paddingTop: space.xl, paddingBottom: space.xxl },
	header: { paddingHorizontal: space.xl },
	sectionHeader: { flexDirection: "row", alignItems: "center", gap: space.md, paddingHorizontal: space.xl, paddingTop: space.xl, paddingBottom: space.xxs },
	sectionTitle: { fontFamily: "DMSans_600SemiBold", color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "600" },
	sectionRule: { flex: 1, height: rowDividerWidth, backgroundColor: t.borderSubtle },
	row: { minHeight: 76, paddingHorizontal: space.xl, paddingVertical: space.sm, gap: space.hair, borderBottomWidth: rowDividerWidth, borderBottomColor: t.borderSubtle },
	eyebrow: { minHeight: 18, flexDirection: "row", alignItems: "center", gap: space.xs },
	spacer: { flex: 1 },
	state: { fontFamily: "DMSans_500Medium", fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "500" },
	title: { fontFamily: "DMSans_600SemiBold", color: t.textPrimary, fontSize: type.callout.fontSize, fontWeight: "600", lineHeight: type.callout.lineHeight, letterSpacing: type.callout.letterSpacing },
	detail: { fontFamily: "DMSans_400Regular", color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
});
