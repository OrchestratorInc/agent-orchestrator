import { StyleSheet, Text, View } from "react-native";
import Svg, { Circle } from "react-native-svg";
import { AppSymbol } from "./AppSymbol";
import { agentAccountInitial, agentAccountPeakUsage, agentAccountUsageSeverity, type AgentAccount } from "./agentAccounts";
import { useTheme } from "./ThemeProvider";
import { font } from "./tokens";

/**
 * An agent account as a letter in a disc, ringed by how much of its fullest
 * usage meter is spent: the accent while there is room, amber from 75%, red
 * from 90%. No ring arc while the machine has not read the meters; a signed
 * out account's letter is dimmed. Without an account (one picked earlier
 * that the machine no longer has, or no default) it is a bare ring around a
 * person, so nothing stands in for an account the task would not start on.
 */
export function AccountUsageAvatar({ account, size = 32, disc: withDisc = true }: {
	account?: Pick<AgentAccount, "email" | "name" | "provider" | "limits" | "signedIn">;
	size?: number;
	/** False draws the letter straight on what is behind it, such as a header button's own circle. */
	disc?: boolean;
}) {
	const t = useTheme();
	const percent = account?.signedIn ? agentAccountPeakUsage(account) : undefined;
	const severity = agentAccountUsageSeverity(percent);
	const stroke = Math.max(2, Math.round(size / 14));
	const r = (size - stroke) / 2;
	const circumference = 2 * Math.PI * r;
	// A sliver stays visible at a few percent, so "read, nearly empty" differs from "not read".
	const filled = percent === undefined ? 0 : Math.max(0.02, percent / 100) * circumference;
	const color = severity === "critical" ? t.red : severity === "warn" ? t.amber : t.accent;
	const disc = size - stroke * 2 - 2;
	// On a button's own circle the letter matches the bar's title.
	const letterSize = withDisc ? Math.round(disc * 0.48) : 17;
	return (
		<View style={{ width: size, height: size }} pointerEvents="none">
			<Svg width={size} height={size} style={StyleSheet.absoluteFill}>
				<Circle cx={size / 2} cy={size / 2} r={r} stroke={t.borderDefault} strokeWidth={stroke} fill="none" />
				{filled > 0 ? (
					<Circle
						cx={size / 2}
						cy={size / 2}
						r={r}
						stroke={color}
						strokeWidth={stroke}
						strokeLinecap="round"
						strokeDasharray={`${filled} ${circumference}`}
						// From twelve o'clock, clockwise.
						transform={`rotate(-90 ${size / 2} ${size / 2})`}
						fill="none"
					/>
				) : null}
			</Svg>
			<View style={[styles.disc, { width: disc, height: disc, borderRadius: disc / 2, top: (size - disc) / 2, left: (size - disc) / 2 }, withDisc && { backgroundColor: t.bgElevated }]}>
				{account ? (
					<Text
						allowFontScaling={false}
						style={{ fontFamily: font.semibold, fontSize: letterSize, lineHeight: Math.round(letterSize * 1.25), color: account.signedIn ? t.textPrimary : t.textTertiary }}
					>
						{agentAccountInitial(account)}
					</Text>
				) : (
					<AppSymbol name={{ ios: "person.fill", android: "person" }} size={letterSize} color={t.textSecondary} />
				)}
			</View>
		</View>
	);
}

const styles = StyleSheet.create({
	disc: { position: "absolute", alignItems: "center", justifyContent: "center" },
});
