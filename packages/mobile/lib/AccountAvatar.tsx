import { useState } from "react";
import { Image, StyleSheet, Text, View } from "react-native";
import { useTheme } from "./ThemeProvider";

/** What the account is called on screen: the person's name, else their email. */
export function accountName(account: { name?: string; email: string }): string {
	return account.name?.trim() || account.email;
}

/** The initials the desktop shows without a photo: a name's first two words, an email's first letter. */
export function accountInitials(account: { name?: string; email: string }): string {
	const name = account.name?.trim();
	if (name) return name.split(/\s+/).slice(0, 2).map((part) => part.charAt(0).toUpperCase()).join("");
	return account.email.charAt(0).toUpperCase();
}

/**
 * The signed-in person as the desktop's account row draws them: the profile
 * photo from the sign-in provider, or their initials on a quiet disc when there
 * is none or it fails to load. Decoration: the row's text names them.
 */
export function AccountAvatar({ account, size }: { account: { name?: string; email: string; avatarUrl?: string }; size: number }) {
	const t = useTheme();
	const [broken, setBroken] = useState(false);
	const shape = { width: size, height: size, borderRadius: size / 2 };
	// The initials are always drawn, the photo over them: a photo still loading,
	// or one that never arrives, leaves the initials rather than a blank disc.
	return (
		<View style={[shape, styles.disc, { backgroundColor: t.bgSurface }]} accessible={false} accessibilityElementsHidden importantForAccessibility="no-hide-descendants">
			<Text allowFontScaling={false} style={[styles.initials, { color: t.textSecondary, fontSize: Math.round(size * 0.4) }]}>
				{accountInitials(account)}
			</Text>
			{account.avatarUrl && !broken ? (
				<Image source={{ uri: account.avatarUrl }} style={[StyleSheet.absoluteFill, shape]} onError={() => setBroken(true)} />
			) : null}
		</View>
	);
}

const styles = StyleSheet.create({
	disc: { alignItems: "center", justifyContent: "center", overflow: "hidden" },
	initials: { fontFamily: "DMSans_600SemiBold", fontWeight: "600" },
});
