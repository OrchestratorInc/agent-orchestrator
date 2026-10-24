import { useCallback, useEffect, useRef } from "react";
import { Pressable, ScrollView, StyleSheet, Text, type LayoutChangeEvent, type NativeScrollEvent, type NativeSyntheticEvent } from "react-native";
import { haptics } from "./haptics";
import { MachineGlyph } from "./MachineIdentity";
import { machineAppearance, sharedMachineSymbols } from "./machineAppearance";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { ALL_WORKER_ENVIRONMENTS, chipRevealOffset, type WorkerEnvironment } from "./worker-controls";
import { iconSize, press, radius, space, type } from "./tokens";

const CHIP_HEIGHT = 32;
// Chips sit below the 44pt target; the slop restores it vertically.
const CHIP_HIT_SLOP = { top: 6, bottom: 6, left: 2, right: 2 };

/**
 * Inline environment filter for the Workers tab: an "All environments" pill and
 * one icon chip per paired machine. The selected chip takes the solid accent
 * fill; idle chips rest on the elevated surface. When two machines share an
 * icon, their chips also carry the name, since the icon alone can't tell them apart.
 * The row scrolls to the selected chip, including a choice made in the filter sheet.
 */
export function WorkerEnvironmentFilter({
	environments,
	selectedEnvironmentId,
	onSelectEnvironment,
}: {
	environments: readonly WorkerEnvironment[];
	selectedEnvironmentId: string;
	onSelectEnvironment: (environmentId: string) => void;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const allSelected = selectedEnvironmentId === ALL_WORKER_ENVIRONMENTS;
	const shared = sharedMachineSymbols(environments);
	const scrollRef = useRef<ScrollView>(null);
	const chipLayouts = useRef(new Map<string, { x: number; width: number }>());
	const scrollOffset = useRef(0);
	const viewportWidth = useRef(0);
	const selectedRef = useRef(selectedEnvironmentId);
	selectedRef.current = selectedEnvironmentId;

	const reveal = useCallback((environmentId: string) => {
		const chip = chipLayouts.current.get(environmentId);
		if (!chip || viewportWidth.current === 0) return;
		const target = chipRevealOffset(chip, { offset: scrollOffset.current, width: viewportWidth.current, inset: space.lg });
		if (target !== null) scrollRef.current?.scrollTo({ x: target, animated: true });
	}, []);
	useEffect(() => reveal(selectedEnvironmentId), [reveal, selectedEnvironmentId]);
	// A chip measured after the selection changed (first render, a name that
	// grew) still has to come into view.
	const onChipLayout = (environmentId: string) => (event: LayoutChangeEvent) => {
		const { x, width } = event.nativeEvent.layout;
		chipLayouts.current.set(environmentId, { x, width });
		if (environmentId === selectedRef.current) reveal(environmentId);
	};
	const select = (environmentId: string) => {
		if (environmentId === selectedEnvironmentId) return;
		haptics.tap();
		onSelectEnvironment(environmentId);
	};
	return (
		<ScrollView
			horizontal
			ref={scrollRef}
			onLayout={(event: LayoutChangeEvent) => {
				viewportWidth.current = event.nativeEvent.layout.width;
				reveal(selectedRef.current);
			}}
			onScroll={(event: NativeSyntheticEvent<NativeScrollEvent>) => { scrollOffset.current = event.nativeEvent.contentOffset.x; }}
			scrollEventThrottle={16}
			showsHorizontalScrollIndicator={false}
			contentContainerStyle={styles.row}
			style={styles.strip}
			testID="worker-environment-strip"
		>
			<Pressable
				accessibilityRole="button"
				accessibilityState={{ selected: allSelected }}
				hitSlop={CHIP_HIT_SLOP}
				onLayout={onChipLayout(ALL_WORKER_ENVIRONMENTS)}
				onPress={() => select(ALL_WORKER_ENVIRONMENTS)}
				style={({ pressed }) => [styles.chip, styles.labelChip, allSelected && styles.selected, pressed && styles.pressed]}
				testID="worker-environment-chip-all"
			>
				<Text numberOfLines={1} style={[styles.label, allSelected && styles.selectedLabel]}>All environments</Text>
			</Pressable>
			{environments.map((environment) => {
				const selected = environment.id === selectedEnvironmentId;
				const { symbol } = machineAppearance({ id: environment.id, name: environment.name, appearance: environment.appearance });
				const labelled = shared.has(symbol);
				return (
					<Pressable
						key={environment.id}
						accessibilityLabel={environment.name}
						accessibilityRole="button"
						accessibilityState={{ selected }}
						hitSlop={CHIP_HIT_SLOP}
						onLayout={onChipLayout(environment.id)}
						// Tapping the selected machine again returns to every environment.
						onPress={() => select(selected ? ALL_WORKER_ENVIRONMENTS : environment.id)}
						style={({ pressed }) => [styles.chip, labelled ? styles.namedChip : styles.iconChip, selected && styles.selected, pressed && styles.pressed]}
						testID={`worker-environment-chip-${environment.id}`}
					>
						<MachineGlyph symbol={symbol} size={iconSize.sm} color={selected ? t.onAccent : t.textSecondary} />
						{labelled ? (
							<Text numberOfLines={1} style={[styles.label, styles.name, selected && styles.selectedLabel]}>{environment.name}</Text>
						) : null}
					</Pressable>
				);
			})}
		</ScrollView>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		strip: { flexGrow: 0 },
		row: { flexDirection: "row", alignItems: "center", gap: space.xs, paddingHorizontal: space.lg, paddingBottom: space.sm },
		chip: { height: CHIP_HEIGHT, borderRadius: radius.pill, alignItems: "center", justifyContent: "center", backgroundColor: t.bgElevated },
		labelChip: { paddingHorizontal: space.md },
		iconChip: { width: CHIP_HEIGHT },
		namedChip: { flexDirection: "row", gap: space.xs, paddingLeft: space.sm + space.hair, paddingRight: space.md },
		name: { maxWidth: 120 },
		selected: { backgroundColor: t.accent },
		pressed: { opacity: press.opacity },
		label: { ...type.footnote, fontFamily: "Geist_500Medium", fontWeight: "500", color: t.textSecondary },
		selectedLabel: { color: t.onAccent },
	});
