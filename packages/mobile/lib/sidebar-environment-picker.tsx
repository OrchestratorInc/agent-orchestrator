import { MenuView, type MenuAction, type NativeActionEvent } from "@expo/ui/community/menu";
import { Feather } from "@expo/vector-icons";
import { ActivityIndicator, StyleSheet, Text, View } from "react-native";
import { useCloudAuth } from "./cloud/authStore";
import { useCloudSignInAction } from "./cloud/useCloudSignInAction";
import { environmentChoiceAction } from "./environment/store";
import type { EnvironmentKind } from "./environment/types";
import { haptics } from "./haptics";
import { sidebarEnvironmentOptions } from "./sidebar-navigation";
import { useEnvironment } from "./store";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles, useThemeState } from "./ThemeProvider";

export function SidebarEnvironmentPicker() {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const { scheme } = useThemeState();
	const { environment, setEnvironment } = useEnvironment();
	const cloudAuth = useCloudAuth();
	const cloudSignIn = useCloudSignInAction();
	const active = environment ?? "local";
	const label = environment === null ? "Environment" : active === "local" ? "Local" : "Cloud";
	const actions: MenuAction[] = sidebarEnvironmentOptions(environment).map((option) => ({
		id: option.id,
		title: option.label,
		state: option.selected ? "on" : "off",
		titleColor: t.textPrimary,
	}));

	const choose = (next: EnvironmentKind) => {
		const action = environmentChoiceAction(environment, next, cloudAuth.signedIn === true);
		if (action === "none") return;
		haptics.select();
		if (action === "sign-in") {
			void cloudSignIn.signIn().then((authenticated) => {
				if (authenticated) setEnvironment("cloud");
			});
			return;
		}
		setEnvironment(next);
	};

	const onPressAction = (event: NativeActionEvent) => {
		const next = event.nativeEvent.event;
		if (next === "local" || next === "cloud") choose(next);
	};

	return (
		<MenuView
			colorScheme={scheme}
			title="Environment"
			actions={actions}
			onPressAction={onPressAction}
			style={styles.menu}
			testID="sidebar-environment-picker"
		>
			<View
				accessible
				accessibilityRole="button"
				accessibilityLabel={`Environment: ${label}`}
				accessibilityHint="Opens the environment menu"
				style={styles.pill}
			>
				<Feather name={active === "local" ? "monitor" : "cloud"} size={16} color={t.textSecondary} />
				<Text numberOfLines={1} style={styles.label}>{label}</Text>
				<View style={styles.spacer} />
				{cloudSignIn.busy ? (
					<ActivityIndicator size="small" color={t.textSecondary} />
				) : (
					<Feather name="chevron-down" size={15} color={t.textTertiary} />
				)}
			</View>
		</MenuView>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	// The mascot artwork includes a tall antenna, so geometric centering leaves
	// the picker visibly above the robot body. Offset to their optical centers.
	menu: { flex: 1, transform: [{ translateY: 8 }] },
	pill: {
		width: "100%",
		height: 44,
		paddingHorizontal: 14,
		borderRadius: 22,
		borderCurve: "continuous",
		borderWidth: StyleSheet.hairlineWidth,
		borderColor: t.borderDefault,
		backgroundColor: t.bgElevated,
		flexDirection: "row",
		alignItems: "center",
		gap: 8,
		overflow: "hidden",
	},
	label: { color: t.textPrimary, fontSize: 15, lineHeight: 20, fontWeight: "700" },
	spacer: { flex: 1 },
});
