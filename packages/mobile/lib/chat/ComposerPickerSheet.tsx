import { Stack } from "expo-router";
import { useMemo, useState } from "react";
import { FlatList, Platform, Pressable, StyleSheet, Text, View } from "react-native";
import { AppSymbol } from "../AppSymbol";
import { rowDividerWidth } from "../divider";
import { haptics } from "../haptics";
import { usePressFeedback } from "../pressFeedback";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { SheetHeader } from "../ui";
import { composerSheetContentStyle } from "./chatLayout";
import { ComposerSearchField } from "./ComposerSearchField";
import { rankComposerCatalog, type ComposerPickerCatalog, type RankedSuggestion } from "./composerSuggestions";
import { iconSize, space, type } from "../tokens";

/**
 * Skills or worktree files to insert into the message. iOS searches in the
 * sheet's navigation bar, which also carries the title; Android docks
 * Material's search field at the top of the list.
 */
export function ComposerPickerSheet({
	catalog,
	initialQuery = "",
	truncated,
	onSelect,
}: {
	catalog: ComposerPickerCatalog;
	initialQuery?: string;
	truncated?: boolean;
	onSelect(value: string): void;
}) {
	const styles = useThemedStyles(makeStyles);
	const [query, setQuery] = useState(initialQuery);
	const choices = useMemo(() => rankComposerCatalog(catalog, query), [catalog, query]);
	const kind = catalog.kind;
	const title = kind === "skills" ? "Skills" : "Worktree files";
	const ios = Platform.OS === "ios";

	return (
		<>
			<Stack.Screen options={{ title }} />
			<FlatList
				style={styles.screen}
				contentContainerStyle={composerSheetContentStyle}
				keyboardShouldPersistTaps="handled"
				keyboardDismissMode="interactive"
				data={choices}
				keyExtractor={(choice) => choice.value}
				// Keeps an Android drag with the list; without it the sheet's own pan
				// takes the gesture and dismisses instead of scrolling back up.
				nestedScrollEnabled
				ListHeaderComponent={(
					<>
						<SheetHeader
							title={ios ? undefined : title}
							subtitle={kind === "skills" ? "Insert a skill into your message." : "Mention a file from this worktree."}
						/>
						{/* iOS puts this in the navigation bar; Android draws it here. */}
						<ComposerSearchField initialQuery={initialQuery} placeholder={kind === "skills" ? "Search skills" : "Search worktree files"} onQuery={setQuery} />
						{kind === "files" && truncated ? (
							<Text style={styles.notice}>Showing the daemon&apos;s capped path list. Narrow your search or type a path directly.</Text>
						) : null}
						<Text style={styles.results}>{choices.length} {kind === "skills" ? (choices.length === 1 ? "skill" : "skills") : (choices.length === 1 ? "file" : "files")}</Text>
					</>
				)}
				ListEmptyComponent={<Text style={styles.empty}>No matches</Text>}
				ItemSeparatorComponent={() => <View style={styles.separator} />}
				renderItem={({ item }) => <SuggestionRow kind={kind} choice={item} onSelect={onSelect} />}
			/>
		</>
	);
}

function SuggestionRow({ kind, choice, onSelect }: { kind: "skills" | "files"; choice: RankedSuggestion; onSelect(value: string): void }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const feedback = usePressFeedback("row");
	return (
		<Pressable
			accessibilityRole="button"
			android_ripple={feedback.android_ripple}
			onPress={() => {
				haptics.select();
				onSelect(choice.value);
			}}
			style={({ pressed }) => [styles.row, pressed && feedback.pressed]}
		>
			<AppSymbol name={kind === "skills" ? { ios: "bolt", android: "bolt" } : "fileText"} size={iconSize.md} color={t.textSecondary} />
			<View style={styles.rowCopy}>
				<Text style={styles.label}>{choice.label}</Text>
				{choice.detail ? <Text numberOfLines={2} style={styles.detail}>{choice.detail}</Text> : null}
			</View>
			{choice.badge ? <Text style={styles.badge}>{choice.badge}</Text> : null}
		</Pressable>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgSurface },
	notice: { fontFamily: type.footnote.fontFamily, color: t.amber, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, marginTop: space.sm },
	results: { fontFamily: "Geist_600SemiBold", color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "600", marginTop: space.xl, marginBottom: space.xxs },
	row: { minHeight: 56, flexDirection: "row", alignItems: "center", gap: space.md, paddingVertical: space.sm, paddingHorizontal: space.hair },
	rowCopy: { flex: 1, minWidth: 0 },
	label: { fontFamily: "Geist_500Medium", color: t.textPrimary, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight, fontWeight: "500" },
	detail: { fontFamily: type.footnote.fontFamily, color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, marginTop: space.hair },
	badge: { fontFamily: type.footnote.fontFamily, color: t.textTertiary, fontSize: type.footnote.fontSize },
	separator: { height: rowDividerWidth, backgroundColor: t.borderSubtle, marginLeft: space.xxxl },
	empty: { fontFamily: type.footnote.fontFamily, color: t.textTertiary, fontSize: type.footnote.fontSize, textAlign: "center", paddingVertical: space.xxl },
});
