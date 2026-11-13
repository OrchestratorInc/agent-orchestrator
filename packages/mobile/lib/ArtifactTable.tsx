import { useMemo, useState } from "react";
import { FlatList, Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import { delimitedTable, nextTableSort, sortTableRows, type TableSort } from "../../shared/files/file-formats";
import { rowDividerWidth } from "./divider";
import { Feather } from "./icons";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { iconSize, space, type } from "./tokens";
import { EmptyState } from "./ui";

const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: "base" });

// A generous character: the header is semibold, and a short header must not wrap.
const CHAR_WIDTH = 8.2;
const COLUMN_MIN = 72;
const COLUMN_MAX = 240;
// Widths come from the header and the first rows: a long file is not measured cell by cell.
const MEASURED_ROWS = 200;

function columnWidth(cells: string[]): number {
	const chars = Math.max(0, ...cells.map((cell) => cell.length));
	// Room for the sort arrow, and the gap before it, beside a header.
	return Math.min(COLUMN_MAX, Math.max(COLUMN_MIN, Math.ceil(chars * CHAR_WIDTH) + space.sm * 2 + space.xxs + iconSize.xs));
}

/**
 * A CSV or TSV artifact as a table, as the desktop shows it: the first row is
 * the header, numeric columns right-align, and tapping a header sorts by that
 * column (ascending, descending, then file order). Wide tables scroll sideways
 * under a header that stays in place; rows are laid out as they scroll into view.
 */
export function ArtifactTable({ content, path }: { content: string; path: string }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const [sort, setSort] = useState<TableSort | null>(null);
	const { header, rows, numeric } = useMemo(() => delimitedTable(content, path), [content, path]);
	const sorted = useMemo(() => sortTableRows(rows, numeric, sort, collator.compare), [numeric, rows, sort]);
	const widths = useMemo(() => header.map((label, column) => columnWidth([label, ...rows.slice(0, MEASURED_ROWS).map((row) => row[column])])), [header, rows]);
	const totalWidth = widths.reduce((sum, width) => sum + width, 0);
	if (header.length === 0) return <View style={styles.center}><EmptyState icon="file" title="This file has no rows" /></View>;
	return (
		// Android: a sideways drag scrolls the table's columns, not the artifact pager it sits in.
		<ScrollView horizontal nestedScrollEnabled style={styles.screen} contentContainerStyle={styles.scroll} showsHorizontalScrollIndicator>
			<View style={[styles.table, { width: Math.max(totalWidth, 0) }]}>
				<View accessibilityRole="header" style={[styles.row, styles.header]}>
					{header.map((label, column) => {
						const direction = sort?.column === column ? sort.direction : undefined;
						return (
							<Pressable
								key={column}
								accessibilityRole="button"
								accessibilityLabel={`${label || `Column ${column + 1}`}, sort`}
								accessibilityState={{ selected: Boolean(direction) }}
								accessibilityValue={direction ? { text: direction } : undefined}
								onPress={() => setSort((current) => nextTableSort(current, column))}
								style={[styles.cell, column === 0 && styles.firstCell, styles.headerCell, numeric[column] && styles.numericHeader, { width: widths[column] }]}
							>
								<Text numberOfLines={2} style={[styles.headerText, !direction && styles.headerIdle, numeric[column] && styles.numeric]}>{label}</Text>
								<Feather name={direction === "descending" ? "arrow-down" : "arrow-up"} size={iconSize.xs} color={direction ? t.textPrimary : "transparent"} />
							</Pressable>
						);
					})}
				</View>
				<FlatList
					data={sorted}
					keyExtractor={(_, index) => String(index)}
					initialNumToRender={40}
					maxToRenderPerBatch={40}
					contentInsetAdjustmentBehavior="automatic"
					renderItem={({ item }) => (
						<View style={styles.row}>
							{item.map((cell, column) => (
								<View key={column} style={[styles.cell, column === 0 && styles.firstCell, { width: widths[column] }]}>
									<Text selectable style={[styles.text, numeric[column] && styles.numeric]}>{cell}</Text>
								</View>
							))}
						</View>
					)}
				/>
			</View>
		</ScrollView>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	center: { flex: 1, alignItems: "center", justifyContent: "center", backgroundColor: t.bgBase },
	scroll: { flexGrow: 1 },
	table: { flex: 1 },
	row: { flexDirection: "row", borderBottomWidth: rowDividerWidth, borderBottomColor: t.borderSubtle },
	header: { backgroundColor: t.bgSubtle },
	cell: { flexShrink: 0, paddingHorizontal: space.sm, paddingVertical: space.xs, borderLeftWidth: rowDividerWidth, borderLeftColor: t.borderSubtle },
	firstCell: { borderLeftWidth: 0 },
	headerCell: { flexDirection: "row", alignItems: "center", gap: space.xxs },
	numericHeader: { flexDirection: "row-reverse" },
	headerText: { flexShrink: 1, fontFamily: "DMSans_600SemiBold", fontWeight: "600", color: t.textPrimary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	headerIdle: { color: t.textSecondary },
	text: { fontFamily: "DMSans_400Regular", color: t.textPrimary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	numeric: { textAlign: "right", fontVariant: ["tabular-nums"] },
});
