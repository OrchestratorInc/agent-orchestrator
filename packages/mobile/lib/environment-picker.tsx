import { MenuView, type MenuAction } from "@expo/ui/community/menu";
import BottomSheet, { BottomSheetScrollView, BottomSheetView } from "@expo/ui/community/bottom-sheet";
import { startTransition, useState } from "react";
import { Platform, Pressable, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { haptics } from "./haptics";
import { Feather } from "./icons";
import { MachineAvatar } from "./MachineIdentity";
import { machineAppearance } from "./machineAppearance";
import { machineMenuImage } from "./machine-menu-image";
import { useTheme, useThemeState, useThemedStyles } from "./ThemeProvider";
import type { Theme } from "./theme";
import { iconSize, press, radius, space, touchTarget, type } from "./tokens";
import { ALL_WORKER_ENVIRONMENTS, workerEnvironmentOptions, type WorkerEnvironment } from "./worker-controls";

export function EnvironmentPicker({ environments, selectedEnvironmentId, onSelectEnvironment }: {
	environments: readonly WorkerEnvironment[];
	selectedEnvironmentId: string;
	onSelectEnvironment: (environmentId: string) => void;
}) {
	const [open, setOpen] = useState(false);
	const insets = useSafeAreaInsets();
	const t = useTheme();
	const { scheme } = useThemeState();
	const styles = useThemedStyles(makeStyles);
	const options = workerEnvironmentOptions(environments);
	const trigger = (
		<Pressable accessibilityRole="button" accessibilityLabel="Switch environment"
			onPress={Platform.OS === "android" ? () => { haptics.tap(); setOpen(true); } : undefined}
			style={({ pressed }) => [styles.trigger, pressed && styles.pressed]}
		>
			<Text style={styles.label}>Switch environment</Text>
			<Feather name="chevron-down" size={iconSize.md} color={t.textSecondary} />
		</Pressable>
	);
	if (Platform.OS === "android") {
		return <>
			{trigger}
			<BottomSheet index={open ? 0 : -1} enablePanDownToClose
				backgroundStyle={{ backgroundColor: t.bgSurface }}
				onClose={() => setOpen(false)}
			>
				<BottomSheetView style={styles.sheet}>
					<View style={styles.header}>
						<Text style={styles.title}>Environments</Text>
						<Pressable accessibilityRole="button" accessibilityLabel="Close environments" onPress={() => setOpen(false)} style={styles.close}>
							<Feather name="x" size={iconSize.lg} color={t.textSecondary} />
						</Pressable>
					</View>
					<BottomSheetScrollView showsVerticalScrollIndicator={false} contentContainerStyle={{ paddingBottom: Math.max(insets.bottom, space.lg) }}>
						<View style={styles.options}>
							{options.map((option, index) => {
								const selected = option.id === selectedEnvironmentId;
								return <Pressable key={option.id} accessibilityRole="radio" accessibilityLabel={option.label} accessibilityState={{ checked: selected }}
									style={({ pressed }) => [styles.option, index > 0 && styles.separator, pressed && styles.pressed]}
									onPress={() => {
										setOpen(false);
										if (selected) return;
										haptics.select();
										onSelectEnvironment(option.id);
									}}
								>
									{option.id === ALL_WORKER_ENVIRONMENTS ? <Feather name="server" size={iconSize.md} color={t.textSecondary} />
										: <MachineAvatar appearance={machineAppearance({ id: option.id, name: option.label, appearance: option.appearance })} size={30} />}
									<Text numberOfLines={1} style={styles.optionLabel}>{option.label}</Text>
									{selected ? <Feather name="check" size={iconSize.lg} color={t.textPrimary} /> : null}
								</Pressable>;
							})}
						</View>
					</BottomSheetScrollView>
				</BottomSheetView>
			</BottomSheet>
		</>;
	}
	const actions: MenuAction[] = options.map((option) => ({
		id: option.id,
		title: option.label,
		image: machineMenuImage(option.id === ALL_WORKER_ENVIRONMENTS ? "server" : machineAppearance({ id: option.id, name: option.label, appearance: option.appearance }).symbol),
		imageColor: t.textSecondary,
		state: option.id === selectedEnvironmentId ? "on" : "off",
	}));
	return (
		<MenuView actions={actions} colorScheme={scheme} title="Environment"
			onPressAction={({ nativeEvent: { event } }) => {
				if (event === selectedEnvironmentId || !options.some((option) => option.id === event)) return;
				haptics.select();
				startTransition(() => onSelectEnvironment(event));
			}}
		>
			{trigger}
		</MenuView>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	trigger: { minHeight: touchTarget, paddingHorizontal: space.lg, flexDirection: "row", alignItems: "center", justifyContent: "center", gap: space.sm, borderWidth: StyleSheet.hairlineWidth, borderColor: t.borderStrong, borderRadius: radius.md },
	label: { ...type.callout, color: t.textPrimary },
	sheet: { paddingHorizontal: space.lg },
	header: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingBottom: space.md },
	title: { ...type.title2, color: t.textPrimary },
	close: { width: touchTarget, height: touchTarget, alignItems: "center", justifyContent: "center" },
	options: { borderRadius: radius.lg, borderCurve: "continuous", backgroundColor: t.bgElevated, overflow: "hidden" },
	option: { minHeight: touchTarget + space.sm, paddingHorizontal: space.lg, flexDirection: "row", alignItems: "center", gap: space.md },
	optionLabel: { ...type.callout, color: t.textPrimary, flex: 1 },
	separator: { borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: t.borderSubtle },
	pressed: { opacity: press.opacity },
});
