import { StyleSheet, View } from "react-native";
import { AgentLogo } from "./AgentLogo";
import { Feather } from "./icons";
import { useTheme } from "./ThemeProvider";

export function SidebarSessionLogo({ harness, pinned }: { harness?: string | null; pinned: boolean }) {
	const t = useTheme();
	return (
		<View testID="sidebar-session-logo" style={styles.logo}>
			<AgentLogo harness={harness} size={28} />
			{pinned ? (
				<View testID="sidebar-session-pin" pointerEvents="none" style={styles.pin}>
					<Feather name="pin" size={12} color={t.textTertiary} fill={t.textTertiary} />
				</View>
			) : null}
		</View>
	);
}

const styles = StyleSheet.create({
	logo: {
		width: 28,
		height: 28,
		flexShrink: 0,
		position: "relative",
	},
	pin: {
		position: "absolute",
		right: -2,
		bottom: -2,
		width: 12,
		height: 12,
		alignItems: "center",
		justifyContent: "center",
		transform: [{ rotate: "45deg" }],
	},
});
