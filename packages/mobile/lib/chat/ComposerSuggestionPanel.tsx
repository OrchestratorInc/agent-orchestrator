import { ActivityIndicator, FlatList, Pressable, StyleSheet, Text, View } from "react-native";
import { AppSymbol } from "../AppSymbol";
import { rowDividerWidth } from "../divider";
import { haptics } from "../haptics";
import { usePressFeedback } from "../pressFeedback";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { iconSize, radius, space, type } from "../tokens";
import { ComposerGlass, composerGlassSupported } from "./composer-glass";
import type { RankedSuggestion } from "./composerSuggestions";

/** About three and a half rows, so the cut-off row says the list scrolls. */
const PANEL_MAX_HEIGHT = 216;
const PANEL_RADIUS = radius.xl;

/**
 * Skills (`/`) or worktree files (`@`) matching what is being typed, docked over
 * the composer. Presentation only: the composer owns the trigger, the query and
 * what a selection inserts, and the field keeps focus the whole time — typing
 * goes on filtering the list rather than moving to a search field of its own.
 *
 * `items` is undefined while the catalog is still loading.
 */
export function ComposerSuggestionPanel({
	kind,
	items,
	truncated,
	onSelect,
}: {
	kind: "skills" | "files";
	items: RankedSuggestion[] | undefined;
	truncated?: boolean;
	onSelect(value: string): void;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const noun = kind === "skills" ? "skills" : "files";
	return (
		<View accessibilityLabel={kind === "skills" ? "Skill suggestions" : "File suggestions"} style={styles.panel}>
			<ComposerGlass radius={PANEL_RADIUS} />
			{items === undefined ? (
				<View style={styles.status}>
					<ActivityIndicator size="small" color={t.textTertiary} />
					<Text style={styles.statusText}>Loading {noun}…</Text>
				</View>
			) : items.length === 0 ? (
				<View style={styles.status}>
					<Text style={styles.statusText}>No {noun} available</Text>
				</View>
			) : (
				<FlatList
					style={styles.list}
					// The field under the keyboard stays focused; a tap on a row must
					// reach the row rather than first dismiss the keyboard.
					keyboardShouldPersistTaps="handled"
					nestedScrollEnabled
					data={items}
					keyExtractor={(item) => item.value}
					ItemSeparatorComponent={Separator}
					renderItem={({ item }) => <SuggestionRow kind={kind} choice={item} onSelect={onSelect} />}
				/>
			)}
			{kind === "files" && truncated && items?.length ? (
				<Text style={styles.notice}>Partial file list. Keep typing to narrow it.</Text>
			) : null}
		</View>
	);
}

function Separator() {
	const styles = useThemedStyles(makeStyles);
	return <View style={styles.separator} />;
}

function SuggestionRow({ kind, choice, onSelect }: { kind: "skills" | "files"; choice: RankedSuggestion; onSelect(value: string): void }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const feedback = usePressFeedback("row");
	const label = kind === "skills" ? `/${choice.label}` : choice.label;
	return (
		<Pressable
			accessibilityRole="button"
			accessibilityLabel={choice.detail ? `${label}, ${choice.detail}` : label}
			android_ripple={feedback.android_ripple}
			onPress={() => {
				haptics.select();
				onSelect(choice.value);
			}}
			style={({ pressed }) => [styles.row, pressed && feedback.pressed]}
		>
			{kind === "files" ? <AppSymbol name="fileText" size={iconSize.sm} color={t.textTertiary} /> : null}
			<View style={styles.rowCopy}>
				<Text numberOfLines={1} style={styles.label}>{label}</Text>
				{choice.detail ? <Text numberOfLines={1} style={styles.detail}>{choice.detail}</Text> : null}
			</View>
			{choice.badge ? <Text numberOfLines={1} style={styles.badge}>{choice.badge}</Text> : null}
		</Pressable>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	panel: {
		overflow: "hidden",
		backgroundColor: composerGlassSupported ? "transparent" : t.bgElevated,
		borderRadius: PANEL_RADIUS,
		borderCurve: "continuous",
		borderWidth: composerGlassSupported ? 0 : rowDividerWidth,
		borderColor: t.borderDefault,
	},
	list: { maxHeight: PANEL_MAX_HEIGHT },
	row: { minHeight: 56, flexDirection: "row", alignItems: "center", gap: space.md, paddingVertical: space.sm, paddingHorizontal: space.lg },
	rowCopy: { flex: 1, minWidth: 0 },
	label: { fontFamily: "DMSans_600SemiBold", color: t.textPrimary, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight, fontWeight: "600" },
	detail: { fontFamily: type.footnote.fontFamily, color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, marginTop: space.hair },
	badge: { fontFamily: type.footnote.fontFamily, color: t.textTertiary, fontSize: type.footnote.fontSize, maxWidth: 96 },
	separator: { height: rowDividerWidth, backgroundColor: t.borderSubtle, marginHorizontal: space.lg },
	status: { minHeight: 56, flexDirection: "row", alignItems: "center", justifyContent: "center", gap: space.sm, paddingHorizontal: space.lg },
	statusText: { fontFamily: type.footnote.fontFamily, color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	notice: { fontFamily: type.footnote.fontFamily, color: t.amber, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, paddingHorizontal: space.lg, paddingVertical: space.xs, borderTopWidth: rowDividerWidth, borderTopColor: t.borderSubtle },
});
