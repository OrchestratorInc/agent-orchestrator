import { Button, Host } from "@expo/ui";
import { useThemeState } from "./ThemeProvider";

export type SpawnFabProps = {
	onPress: () => void;
	testID: string;
};

/**
 * The Workers dock's spawn button on its own, for screens that float it in the
 * bottom-right corner without the rest of the dock.
 */
export function SpawnFab({ onPress, testID }: SpawnFabProps) {
	const { scheme } = useThemeState();

	return (
		<Host style={{ width: 52, height: 52 }} colorScheme={scheme}>
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
