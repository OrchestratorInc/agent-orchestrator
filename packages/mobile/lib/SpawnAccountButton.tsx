import { Host } from "@expo/ui";
import { Circle } from "@expo/ui/swift-ui";
import { foregroundStyle, frame } from "@expo/ui/swift-ui/modifiers";
import { Stack } from "expo-router";
import { useMemo } from "react";
import { Platform, Pressable, StyleSheet, View } from "react-native";
import { AccountUsageAvatar } from "./AccountUsageAvatar";
import { accountUsageAccessibilityLabel, agentAccountLabel, type AgentAccount } from "./agentAccounts";
import { glassCircle } from "./glass";
import { haptics } from "./haptics";
import { HEADER_PILL_SIZE, HeaderPillButton, PillHeaderRight } from "./headerPill";
import { useThemeState } from "./ThemeProvider";
import { touchTarget } from "./tokens";

// The back button's glass circle (`GLASS_CIRCLE_SIZE` in native-header-button.ios).
const GLASS_CIRCLE_SIZE = touchTarget;

/**
 * The account a new task starts on, in the bar's trailing corner: the same
 * circle as the back button opposite (glass on iOS, the pill on Android),
 * holding the account's letter, with its usage traced around the edge. A tap
 * opens the machine's accounts to pick another. Nothing while the agent has
 * no signed-in account (cloud, other agents, older machines).
 */
export function SpawnAccountButton({ account, accounts, onPress }: { account?: AgentAccount; accounts: readonly AgentAccount[]; onPress(): void }) {
	const label = accountUsageAccessibilityLabel(account ? agentAccountLabel(account, accounts) : undefined, account);
	// Offered whenever there is an account to pick, even while the one picked
	// (a restored draft's, since removed) or a default is missing: the sheet is
	// the only way to replace it.
	const shown = Boolean(account) || accounts.some((candidate) => candidate.signedIn);
	const press = useMemo(() => () => { haptics.tap(); onPress(); }, [onPress]);
	const pill = useMemo(() => (Platform.OS === "android" && shown ? (
		<HeaderPillButton label={label} onPress={press} testID="spawn-account">
			<AccountUsageAvatar account={account} size={HEADER_PILL_SIZE} disc={false} />
		</HeaderPillButton>
	) : null), [account, label, press, shown]);
	const toolbar = useMemo(() => (Platform.OS === "ios" && shown ? (
		<Stack.Toolbar placement="right">
			<Stack.Toolbar.View hidesSharedBackground>
				<GlassAvatarButton account={account} label={label} onPress={press} />
			</Stack.Toolbar.View>
		</Stack.Toolbar>
	) : null), [account, label, press, shown]);
	return Platform.OS === "android" ? <PillHeaderRight>{pill}</PillHeaderRight> : toolbar;
}

/**
 * The back button's glass circle (`NativeHeaderButton`), drawn under the
 * avatar. The touch is React Native's: the SwiftUI layer takes none, so the
 * whole circle is one button with the avatar's label.
 */
function GlassAvatarButton({ account, label, onPress }: { account?: AgentAccount; label: string; onPress(): void }) {
	const { scheme } = useThemeState();
	return (
		<Pressable
			accessibilityRole="button"
			accessibilityLabel={label}
			onPress={onPress}
			testID="spawn-account"
			style={({ pressed }) => [styles.circle, pressed && styles.pressed]}
		>
			<View pointerEvents="none" style={StyleSheet.absoluteFill} importantForAccessibility="no-hide-descendants" accessibilityElementsHidden>
				<Host style={styles.circle} colorScheme={scheme}>
					<Circle modifiers={[frame({ width: GLASS_CIRCLE_SIZE, height: GLASS_CIRCLE_SIZE }), foregroundStyle("transparent"), glassCircle(undefined, false)]} />
				</Host>
			</View>
			<AccountUsageAvatar account={account} size={GLASS_CIRCLE_SIZE} disc={false} />
		</Pressable>
	);
}

const styles = StyleSheet.create({
	circle: { width: GLASS_CIRCLE_SIZE, height: GLASS_CIRCLE_SIZE },
	pressed: { opacity: 0.6 },
});
