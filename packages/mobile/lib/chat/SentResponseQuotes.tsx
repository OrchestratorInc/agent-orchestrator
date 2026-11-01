import { Text, View } from "react-native";
import { useTheme } from "../ThemeProvider";
import { prose, space, type } from "../tokens";
import type { ConversationContentSummary } from "./types";

/** Desktop uses an annotation chip; mobile keeps the referenced text readable. */
export function SentResponseQuotes({ content }: { content?: readonly ConversationContentSummary[] }) {
	const t = useTheme();
	const excerpts = content?.filter((item) => item.type === "excerpt" && item.text) ?? [];
	if (!excerpts.length) return null;
	return <View style={{ gap: space.md, marginBottom: space.md, paddingBottom: space.md, borderBottomWidth: 1, borderBottomColor: t.borderSubtle }}>
		{excerpts.map((excerpt, index) => <View key={index} style={{ flexDirection: "row", alignItems: "flex-start", gap: space.sm }}>
			<Text accessible={false} accessibilityElementsHidden importantForAccessibility="no" style={{ ...type.title2, lineHeight: prose.lineHeight, color: t.textTertiary }}>“</Text>
			<Text selectable accessibilityLabel={`Selected response: ${excerpt.text}`} style={{ ...prose, flex: 1, minWidth: 0, color: t.textSecondary }}>{excerpt.text}</Text>
		</View>)}
	</View>;
}
