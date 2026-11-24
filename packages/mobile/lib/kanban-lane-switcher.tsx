import { useEffect, useMemo, useState } from "react";
import { Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import type { DashboardSession } from "./api";
import { haptics } from "./haptics";
import { initialLane, KANBAN_LANES, laneCounts, laneLabel, type KanbanLane } from "./kanbanLanes";
import type { Theme } from "./theme";
import { useThemedStyles } from "./ThemeProvider";
import { font, space } from "./tokens";

/**
 * Which lane a board shows. It opens on the lane that needs you, follows the
 * user's choice after that, and starts over when the board's set of sessions
 * changes wholesale (a different project or machine filter).
 */
export function useKanbanLane(sessions: readonly DashboardSession[], identityKey: string) {
	const [chosen, setChosen] = useState<{ key: string; lane: KanbanLane }>();
	const counts = useMemo(() => laneCounts(sessions), [sessions]);
	const opening = useMemo(() => initialLane(sessions), [sessions]);
	const [opened, setOpened] = useState<{ key: string; lane: KanbanLane }>();
	// The opening lane is fixed once sessions arrive, so a poll moving a card
	// never moves the board under the user.
	const live = Object.values(counts).some((count) => count > 0);
	useEffect(() => {
		if (live && opened?.key !== identityKey) setOpened({ key: identityKey, lane: opening });
	}, [identityKey, live, opened?.key, opening]);
	const lane = chosen?.key === identityKey ? chosen.lane : opened?.key === identityKey ? opened.lane : opening;
	return { lane, counts, setLane: (next: KanbanLane) => setChosen({ key: identityKey, lane: next }) };
}

/**
 * The desktop board's lanes as a row of chips, each with its count; the
 * board shows the chosen lane's cards. Lanes are switched by tapping, not by
 * swiping: a board row already swipes to pin and archive.
 */
export function KanbanLaneSwitcher({ lane, counts, onSelect }: { lane: KanbanLane; counts: Record<KanbanLane, number>; onSelect(lane: KanbanLane): void }) {
	const styles = useThemedStyles(makeStyles);
	return (
		<View style={styles.wrap} accessibilityRole="tablist" testID="kanban-lanes">
			<ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={styles.row}>
				{KANBAN_LANES.map((item) => {
					const on = item === lane;
					const label = laneLabel(item);
					return (
						<Pressable
							key={item}
							accessibilityRole="tab"
							accessibilityState={{ selected: on }}
							accessibilityLabel={`${label}, ${counts[item]} session${counts[item] === 1 ? "" : "s"}`}
							onPress={() => { if (!on) { haptics.select(); onSelect(item); } }}
							style={[styles.chip, on && styles.chipOn]}
							testID={`kanban-lane-${item}`}
						>
							<Text style={[styles.label, on && styles.labelOn]} numberOfLines={1}>{label}</Text>
							<Text style={[styles.count, on && styles.labelOn]}>{counts[item]}</Text>
						</Pressable>
					);
				})}
			</ScrollView>
		</View>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	wrap: { paddingBottom: space.sm },
	row: { paddingHorizontal: space.lg, gap: space.xs },
	chip: { height: 32, flexDirection: "row", alignItems: "center", gap: space.xs, paddingHorizontal: space.md, borderRadius: 16, borderWidth: StyleSheet.hairlineWidth, borderColor: t.borderDefault },
	chipOn: { backgroundColor: t.accentTint, borderColor: t.accentBorder },
	label: { fontFamily: font.medium, fontSize: 13, color: t.textSecondary },
	labelOn: { color: t.textPrimary },
	count: { fontFamily: font.medium, fontSize: 12, color: t.textTertiary, fontVariant: ["tabular-nums"] },
});
