import BottomSheet, { BottomSheetView } from "@expo/ui/community/bottom-sheet";
import { useLocalSearchParams, useRouter } from "expo-router";
import { useEffect, useRef, useState } from "react";
import { Platform, StyleSheet, Text, TextInput, View } from "react-native";
import { backOr } from "../../lib/backNavigation";
import { haptics } from "../../lib/haptics";
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
	const { resultKey, suggested } = useLocalSearchParams<{ resultKey?: string; suggested?: string }>();
	const [name, setName] = useState((suggested ?? "").slice(0, 48));
	const decided = useRef(false);
	const openingHapticPlayed = useRef(false);

	useEffect(() => {
		if (openingHapticPlayed.current) return;
		openingHapticPlayed.current = true;
		haptics.tap();
	}, []);

	useEffect(
		() => () => {
			if (!decided.current) takeSheetResult<string | null>(resultKey)?.(null);
			releaseSheetResult(resultKey);
		},
		[resultKey],
	);

	function save() {
		const trimmed = name.trim();
		if (!trimmed) return;
		decided.current = true;
		const handler = takeSheetResult<string | null>(resultKey);
		backOr(router);
		handler?.(trimmed);
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
