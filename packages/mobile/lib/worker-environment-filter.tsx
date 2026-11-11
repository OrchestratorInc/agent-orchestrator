import { useCallback, useEffect, useRef } from "react";
import { Pressable, ScrollView, StyleSheet, Text, View, type LayoutChangeEvent, type NativeScrollEvent, type NativeSyntheticEvent } from "react-native";
import { AppSymbol } from "./AppSymbol";
import { haptics } from "./haptics";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { gutter, press, radius, space, touchTarget, type } from "./tokens";
import { ALL_WORKER_ENVIRONMENTS, workerEnvironmentOptions } from "./worker-controls";
import type { WorkerEnvironmentFilterProps } from "./worker-environment-filter.types";

export type { WorkerEnvironmentFilterProps } from "./worker-environment-filter.types";

/** A chip's resting height; the hit slop brings it to the touch target. */
const CHIP_HEIGHT = 32;
const CHIP_HIT_SLOP = { top: (touchTarget - CHIP_HEIGHT) / 2, bottom: (touchTarget - CHIP_HEIGHT) / 2, left: 2, right: 2 };

/**
 * The Workers board's machine filter on iOS: "All machines" and one chip per
 * paired machine, scrolling sideways, as Android's Material filter chips do.
 * It is the one place to choose a machine; the header's filter holds projects.
 * The chosen chip is tinted and checked, the others outlined, and choosing the
 * chosen machine again goes back to every machine.
 */
export function WorkerEnvironmentFilter({ environments, selectedEnvironmentId, onSelectEnvironment }: WorkerEnvironmentFilterProps) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const scrollRef = useRef<ScrollView>(null);
	const chips = useRef(new Map<string, { x: number; width: number }>());
	const offset = useRef(0);
	const viewport = useRef(0);
	const selectedRef = useRef(selectedEnvironmentId);
	selectedRef.current = selectedEnvironmentId;

	// The chosen chip in view, also when it was chosen before the row was shown.
	const reveal = useCallback((environmentId: string) => {
		const chip = chips.current.get(environmentId);
		if (!chip || viewport.current === 0) return;
		const start = chip.x - gutter;
		const end = chip.x + chip.width + gutter - viewport.current;
		if (start < offset.current) scrollRef.current?.scrollTo({ x: Math.max(0, start), animated: true });
		else if (end > offset.current) scrollRef.current?.scrollTo({ x: end, animated: true });
	}, []);
	useEffect(() => reveal(selectedEnvironmentId), [reveal, selectedEnvironmentId]);

	const select = (environmentId: string) => {
		if (environmentId === selectedEnvironmentId) return;
		haptics.select();
		onSelectEnvironment(environmentId);
	};

	return (
		<ScrollView
			horizontal
			ref={scrollRef}
			showsHorizontalScrollIndicator={false}
			onLayout={(event: LayoutChangeEvent) => {
				viewport.current = event.nativeEvent.layout.width;
				reveal(selectedRef.current);
			}}
			onScroll={(event: NativeSyntheticEvent<NativeScrollEvent>) => { offset.current = event.nativeEvent.contentOffset.x; }}
			scrollEventThrottle={16}
			style={styles.strip}
			contentContainerStyle={styles.row}
			testID="worker-environment-strip"
		>
			{workerEnvironmentOptions(environments).map((environment) => {
				const all = environment.id === ALL_WORKER_ENVIRONMENTS;
				const selected = environment.id === selectedEnvironmentId;
				return (
					<Pressable
						key={environment.id}
						accessibilityRole="button"
						accessibilityLabel={environment.label}
						accessibilityState={{ selected }}
						hitSlop={CHIP_HIT_SLOP}
						onLayout={(event) => {
							const { x, width } = event.nativeEvent.layout;
							chips.current.set(environment.id, { x, width });
							if (environment.id === selectedRef.current) reveal(environment.id);
						}}
						onPress={() => select(selected && !all ? ALL_WORKER_ENVIRONMENTS : environment.id)}
						style={({ pressed }) => [styles.chip, selected ? styles.selected : styles.idle, pressed && styles.pressed]}
						testID={`worker-environment-chip-${environment.id}`}
					>
						{selected ? (
							<View style={styles.check}>
								<AppSymbol name="checkmark" size={14} color={t.accent} />
							</View>
						) : null}
						<Text numberOfLines={1} style={[styles.label, selected && styles.selectedLabel]}>{environment.label}</Text>
					</Pressable>
				);
			})}
		</ScrollView>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		strip: { flexGrow: 0 },
		row: { flexDirection: "row", alignItems: "center", gap: space.sm, paddingHorizontal: gutter, paddingBottom: space.sm },
		chip: {
			height: CHIP_HEIGHT,
			flexDirection: "row",
			alignItems: "center",
			gap: space.xs,
			paddingHorizontal: space.md,
			borderRadius: radius.pill,
			borderCurve: "continuous",
		},
		idle: { borderWidth: StyleSheet.hairlineWidth * 2, borderColor: t.borderDefault },
		selected: { backgroundColor: t.accentTint },
		pressed: { opacity: press.opacity },
		check: { marginLeft: -space.xxs },
		label: { ...type.subheadline, fontFamily: "DMSans_500Medium", fontWeight: "500", color: t.textSecondary, maxWidth: 160 },
		selectedLabel: { color: t.accent },
	});
