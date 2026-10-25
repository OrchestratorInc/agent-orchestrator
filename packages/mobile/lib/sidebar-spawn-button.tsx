import { Button, Host } from "@expo/ui";
import { useThemeState } from "./ThemeProvider";

export function SidebarSpawnButton({ onPress }: { onPress: () => void }) {
	const { scheme } = useThemeState();

	return (
		<Host style={{ width: 48, height: 48 }} colorScheme={scheme}>
			<Button
				label="+"
				onPress={onPress}
				testID="sidebar-spawn-worker"
				variant="outlined"
				style={{ width: 48, height: 48, borderRadius: 20, backgroundColor: "transparent" }}
			/>
		</Host>
	);
}
