import { Host } from "@expo/ui";
import { Button, Group, Image } from "@expo/ui/swift-ui";
import {
	accessibilityIdentifier,
	accessibilityLabel,
	buttonStyle,
	controlSize,
	frame,
} from "@expo/ui/swift-ui/modifiers";
import { glassCircle } from "./glass";
import { GLASS_CIRCLE_SIZE } from "./native-header-button.ios";
import type { SpawnFabProps } from "./spawn-fab";
import { useTheme, useThemeState } from "./ThemeProvider";

/**
 * Matches the spawn circle in `worker-dock.ios.tsx`: as wide as the circle so
 * it meets the same right edge, as tall as the dock so it sits at the same height.
 */
export function SpawnFab({ onPress, testID }: SpawnFabProps) {
	const t = useTheme();
	const { scheme } = useThemeState();

	return (
		<Host style={{ width: GLASS_CIRCLE_SIZE, height: 52 }} colorScheme={scheme} seedColor={t.accent}>
			<Group
				modifiers={[
					frame({ width: GLASS_CIRCLE_SIZE, height: GLASS_CIRCLE_SIZE }),
					glassCircle(),
				]}
			>
				<Button
					onPress={onPress}
					modifiers={[
						buttonStyle("plain"),
						controlSize("large"),
						frame({ width: GLASS_CIRCLE_SIZE, height: GLASS_CIRCLE_SIZE }),
						accessibilityLabel("Spawn worker"),
						accessibilityIdentifier(testID),
					]}
				>
					<Image
						systemName="plus"
						size={18}
						color={t.textPrimary}
						modifiers={[frame({ width: GLASS_CIRCLE_SIZE, height: GLASS_CIRCLE_SIZE })]}
					/>
				</Button>
			</Group>
		</Host>
	);
}
