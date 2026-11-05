import { Button, Host } from "@expo/ui";
import { useTheme, useThemeState } from "./ThemeProvider";

export type SpawnFabProps = {
	onPress: () => void;
	testID: string;
};

/**
 * The Workers dock's spawn button on its own, for screens that float it in the
 * bottom-right corner without the rest of the dock.
 */
export function SpawnFab({ onPress, testID }: SpawnFabProps) {
	const t = useTheme();
	const { scheme } = useThemeState();

	return (
		<Host style={{ width: 52, height: 52 }} colorScheme={scheme} seedColor={t.accent}>
			<Button
				label="+"
				onPress={onPress}
				testID={testID}
				variant="outlined"
				style={{ width: 52, height: 52, borderRadius: 28 }}
			/>
		</Host>
	);
}
