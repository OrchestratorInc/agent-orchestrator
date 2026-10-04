import { View } from "react-native";
import { environmentBadgeStyle } from "./environment-badge";
import { Feather } from "./icons";
import type { Theme } from "./theme";

export function EnvironmentBadge({ sourceLabel, theme }: { sourceLabel: "Local" | "Cloud"; theme: Theme }) {
	return (
		<View accessible accessibilityRole="image" accessibilityLabel={`${sourceLabel} environment`} style={environmentBadgeStyle()}>
			<Feather name={sourceLabel === "Cloud" ? "cloud" : "server"} size={14} color={theme.textPrimary} />
		</View>
	);
}
