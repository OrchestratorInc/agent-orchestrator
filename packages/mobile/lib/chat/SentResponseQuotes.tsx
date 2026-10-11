import { Text, View } from "react-native";
import { useTheme } from "../ThemeProvider";
import { space, type } from "../tokens";
import type { ConversationContentSummary } from "./types";

/** Desktop uses an annotation chip; mobile keeps the referenced text readable. */
export function SentResponseQuotes({ content }: { content?: readonly ConversationContentSummary[] }) {
	const t = useTheme();
	const excerpts = content?.filter((item) => item.type === "excerpt" && item.text) ?? [];
	if (!excerpts.length) return null;
	return <View style={{ gap: space.xs, marginBottom: space.sm }}>
		{excerpts.map((excerpt, index) => <Text key={index} selectable accessibilityLabel={`Selected response: ${excerpt.text}`} style={{ ...type.subheadline, color: t.textSecondary, backgroundColor: t.accentTint, borderRadius: 6, padding: space.xs }}>{excerpt.text}</Text>)}
	</View>;
}
