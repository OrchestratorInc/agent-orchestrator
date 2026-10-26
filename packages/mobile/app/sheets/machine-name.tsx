import BottomSheet, { BottomSheetView } from "@expo/ui/community/bottom-sheet";
import { Stack, useLocalSearchParams, useRouter } from "expo-router";
import { useEffect, useMemo, useRef, useState } from "react";
import { Platform, StyleSheet, View } from "react-native";
import { backOr } from "../../lib/backNavigation";
import { haptics } from "../../lib/haptics";
import { loadHosts, type Host } from "../../lib/hosts";
import { machineAppearance, machineAppearancePalette, unusedMachineSymbol, type MachineSymbol } from "../../lib/machineAppearance";
import { MachineNameForm } from "../../lib/MachineNameForm";
import type { MachineNameChoice } from "../../lib/pairFlow";
import { releaseSheetResult, takeSheetResult } from "../../lib/sheetResult";
import { useTheme } from "../../lib/ThemeProvider";

export default function MachineNameSheetRoute() {
	const router = useRouter();
	const t = useTheme();
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

	// iOS titles the sheet in its native bar, with Cancel and Save there as a
	// form sheet's are. Memoized on whether Save is enabled, so typing does not
	// register the header again on every key; Save reads the latest draft. Save
	// is semibold rather than the done style, which iOS 26 fills with the tint,
	// near white in the dark theme.
	const ios = Platform.OS === "ios";
	const canSave = name.trim().length > 0;
	const saveRef = useRef(save);
	saveRef.current = save;
	const headerItems = useMemo(() => ios ? (
		<>
			<Stack.Toolbar placement="left">
				<Stack.Toolbar.Button onPress={() => backOr(router)}>Cancel</Stack.Toolbar.Button>
			</Stack.Toolbar>
			<Stack.Toolbar placement="right">
				<Stack.Toolbar.Button accessibilityLabel="Save machine" disabled={!canSave} style={{ fontWeight: "600" }} onPress={() => saveRef.current()}>Save</Stack.Toolbar.Button>
			</Stack.Toolbar>
		</>
	) : null, [canSave, ios, router]);

	const content = (
		<MachineNameForm
			name={name}
			onChangeName={setName}
			symbol={symbol}
			onSelectSymbol={pickSymbol}
			iconHint={sharingSymbol.length > 0 ? `Also used by ${formatNames(sharingSymbol)}. The name will tell them apart.` : undefined}
			canSave={canSave}
			onSave={() => saveRef.current()}
		/>
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

	return (
		<>
			{headerItems}
			{content}
		</>
	);
}

function formatNames(names: readonly string[]): string {
	if (names.length <= 2) return names.join(" and ");
	return `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}`;
}

const styles = StyleSheet.create({
	androidModalRoot: { flex: 1, backgroundColor: "transparent" },
});

export { SheetErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";
