import BottomSheet, { BottomSheetView } from "@expo/ui/community/bottom-sheet";
import { useLocalSearchParams, useRouter } from "expo-router";
import { useEffect, useMemo, useRef, useState } from "react";
import { Platform, StyleSheet, Text, TextInput, View } from "react-native";
import { backOr } from "../../lib/backNavigation";
import { haptics } from "../../lib/haptics";
import { loadHosts, type Host } from "../../lib/hosts";
import { machineAppearance, machineAppearancePalette, machineSymbolLabels, unusedMachineSymbol, type MachineSymbol } from "../../lib/machineAppearance";
import { MachineSymbolPicker } from "../../lib/MachineSymbolPicker";
import type { MachineNameChoice } from "../../lib/pairFlow";
import { Button, SheetScreen } from "../../lib/ui";
import { releaseSheetResult, takeSheetResult } from "../../lib/sheetResult";
import type { Theme } from "../../lib/theme";
import { useTheme, useThemedStyles } from "../../lib/ThemeProvider";
import { textSelectionColors } from "../../lib/textSelection";
import { space, type } from "../../lib/tokens";

export default function MachineNameSheetRoute() {
	const router = useRouter();
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const { resultKey, suggested, symbol: suggestedSymbol, hostId } = useLocalSearchParams<{ resultKey?: string; suggested?: string; symbol?: string; hostId?: string }>();
	const [name, setName] = useState((suggested ?? "").slice(0, 48));
	const [symbol, setSymbol] = useState<MachineSymbol>(
		() => machineAppearancePalette.symbols.find((candidate) => candidate === suggestedSymbol) ?? "server",
	);
	const [otherMachines, setOtherMachines] = useState<Host[]>([]);
	const symbolTouched = useRef(false);
	const decided = useRef(false);

	// Start on an icon no other machine uses, unless the user has already picked one.
	useEffect(() => {
		let cancelled = false;
		void loadHosts().then((hosts) => {
			if (cancelled) return;
			const others = hosts.filter((host) => host.id !== hostId);
			setOtherMachines(others);
			if (symbolTouched.current) return;
			const taken = new Set(others.map((host) => machineAppearance(host).symbol));
			setSymbol((current) => unusedMachineSymbol(current, taken));
		}).catch(() => {});
		return () => { cancelled = true; };
	}, [hostId]);

	const sharingSymbol = useMemo(
		() => otherMachines.filter((host) => machineAppearance(host).symbol === symbol).map((host) => host.name),
		[otherMachines, symbol],
	);
	function pickSymbol(next: MachineSymbol) {
		symbolTouched.current = true;
		setSymbol(next);
	}
	const openingHapticPlayed = useRef(false);

	useEffect(() => {
		if (openingHapticPlayed.current) return;
		openingHapticPlayed.current = true;
		haptics.tap();
	}, []);

	useEffect(
		() => () => {
			if (!decided.current) takeSheetResult<MachineNameChoice | null>(resultKey)?.(null);
			releaseSheetResult(resultKey);
		},
		[resultKey],
	);

	function save() {
		const trimmed = name.trim();
		if (!trimmed) return;
		decided.current = true;
		const handler = takeSheetResult<MachineNameChoice | null>(resultKey);
		backOr(router);
		handler?.({ name: trimmed, appearance: { symbol } });
	}

	const content = (
		<SheetScreen title="Name this machine" subtitle="Choose a name you'll recognize in Orchestrator.">
			<View style={styles.field}>
				<Text style={styles.label}>MACHINE NAME</Text>
				<TextInput
					autoFocus
					value={name}
					onChangeText={setName}
					placeholder="Studio Mac"
					placeholderTextColor={t.textFaint}
					{...textSelectionColors(t)}
					maxLength={48}
					returnKeyType="done"
					onSubmitEditing={save}
					style={styles.input}
				/>
			</View>
			<View style={styles.field}>
				<Text style={styles.label}>
					ICON <Text style={styles.labelValue}>· {machineSymbolLabels[symbol]}</Text>
				</Text>
				<MachineSymbolPicker testIDPrefix="machine-name-symbol" selected={symbol} onSelect={pickSymbol} />
				{sharingSymbol.length > 0 ? (
					<Text testID="machine-name-symbol-shared" style={styles.hint}>
						Also used by {formatNames(sharingSymbol)}. The name will tell them apart.
					</Text>
				) : null}
			</View>
			<Button title="Save machine" disabled={!name.trim()} onPress={save} style={styles.button} />
		</SheetScreen>
	);

	if (Platform.OS === "android") {
		// Unlike the manual connection form, this compact prompt must not expand
		// into a tall fixed detent when focused. Material sizes it to the form and
		// handles the IME inset in its own window; don't add another keyboard pad.
		return (
			<View style={styles.androidModalRoot}>
				<BottomSheet
					index={0}
					enableDynamicSizing
					enablePanDownToClose
					backgroundStyle={{ backgroundColor: t.bgSurface }}
					onClose={() => backOr(router)}
				>
					<BottomSheetView>{content}</BottomSheetView>
				</BottomSheet>
			</View>
		);
	}

	return content;
}

function formatNames(names: readonly string[]): string {
	if (names.length <= 2) return names.join(" and ");
	return `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}`;
}

const makeStyles = (t: Theme) => StyleSheet.create({
	androidModalRoot: { flex: 1, backgroundColor: "transparent" },
	field: { marginTop: space.lg },
	label: {
		fontFamily: "Geist_600SemiBold",
		color: t.textTertiary,
		fontSize: type.caption2.fontSize,
		fontWeight: "600",
		letterSpacing: 1.1,
		marginBottom: space.xs,
	},
	hint: { fontFamily: "Geist_400Regular", color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, marginTop: space.sm },
	labelValue: { fontFamily: "Geist_500Medium", color: t.textSecondary, fontWeight: "500", letterSpacing: 0 },
	input: {
		fontFamily: "Geist_400Regular",
		backgroundColor: t.bgElevated,
		borderWidth: 1,
		borderColor: t.borderDefault,
		borderRadius: 8,
		borderCurve: "continuous",
		paddingHorizontal: space.md,
		paddingVertical: space.md,
		color: t.textPrimary,
		fontSize: type.subheadline.fontSize,
	},
	button: { marginTop: space.lg },
});

export { SheetErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";
