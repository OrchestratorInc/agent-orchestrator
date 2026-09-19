import { Feather } from "@expo/vector-icons";
import { useState } from "react";
import { ActivityIndicator, Pressable, StyleSheet, Text, TextInput, View } from "react-native";
import { useCloudAuth } from "./cloud/authStore";
import { haptics } from "./haptics";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { SheetScreen } from "./ui";

type Mode = "sign-in" | "register";

/**
 * Sign in to (or register on) AO Cloud with email and password.
 *
 * The control plane's production sign-in is WorkOS (see lib/cloud/signIn.ts's
 * buildWorkOSAuthUrl/exchangeWorkOSCode), which needs an in-app browser and a
 * deep-link callback this pass does not add — neither expo-web-browser nor
 * expo-crypto (for the PKCE verifier) is installed, and wiring an unverified
 * OAuth redirect blind, with no device to test it on, is worse than not
 * offering it. Email/password against the same control plane
 * (lib/cloud/signIn.ts's signInWithLocalAuth/registerWithLocalAuth) is real
 * today and is what this sheet uses.
 */
export function CloudSignInSheet({ onDone, onClose }: { onDone: () => void; onClose: () => void }) {
	const t = useTheme();
	const s = useThemedStyles(makeStyles);
	const cloudAuth = useCloudAuth();
	const [mode, setMode] = useState<Mode>("sign-in");
	const [email, setEmail] = useState("");
	const [password, setPassword] = useState("");
	const [displayName, setDisplayName] = useState("");
	const [submitting, setSubmitting] = useState(false);
	const [error, setError] = useState<string | null>(null);

	const trimmedEmail = email.trim();
	const canSubmit =
		!submitting &&
		trimmedEmail.length > 0 &&
		password.length > 0 &&
		(mode === "sign-in" || displayName.trim().length > 0);

	async function submit() {
		if (!canSubmit) return;
		setSubmitting(true);
		setError(null);
		try {
			if (mode === "sign-in") {
				await cloudAuth.signInLocal(trimmedEmail, password);
			} else {
				const name = displayName.trim();
				// The control plane derives a URL-safe slug from the display name;
				// it requires a non-empty orgSlug/orgName distinct from the account
				// name, so a first workspace is named after its owner.
				const slug = name.toLowerCase().replace(/[^a-z0-9-]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 63) || "workspace";
				await cloudAuth.registerLocal({
					email: trimmedEmail,
					password,
					displayName: name,
					orgSlug: slug,
					orgName: name,
				});
			}
			onDone();
		} catch (cause) {
			setError(cause instanceof Error ? cause.message : "That didn't work.");
		} finally {
			setSubmitting(false);
		}
	}

	return (
		<SheetScreen
			title="Sign in to AO Cloud"
			subtitle="Email and password sign-in against your AO Cloud account."
		>
			<View style={{ paddingTop: 8, gap: 14 }}>
				<View style={s.row}>
					{(["sign-in", "register"] as const).map((option) => {
						const selected = option === mode;
						return (
							<Pressable
								key={option}
								accessibilityRole="button"
								accessibilityState={{ selected }}
								onPress={() => {
									haptics.select();
									setMode(option);
									setError(null);
								}}
								style={[s.chip, selected && { borderColor: t.blue }]}
							>
								<Text style={[s.chipText, selected && { color: t.blue }]}>
									{option === "sign-in" ? "Sign in" : "Create account"}
								</Text>
							</Pressable>
						);
					})}
				</View>

				{mode === "register" ? (
					<TextInput
						value={displayName}
						onChangeText={setDisplayName}
						placeholder="Your name"
						placeholderTextColor={t.textFaint}
						autoCapitalize="words"
						editable={!submitting}
						style={s.input}
					/>
				) : null}
				<TextInput
					value={email}
					onChangeText={setEmail}
					placeholder="you@example.com"
					placeholderTextColor={t.textFaint}
					autoCapitalize="none"
					autoCorrect={false}
					keyboardType="email-address"
					textContentType="username"
					editable={!submitting}
					style={s.input}
				/>
				<TextInput
					value={password}
					onChangeText={setPassword}
					placeholder="Password"
					placeholderTextColor={t.textFaint}
					autoCapitalize="none"
					autoCorrect={false}
					secureTextEntry
					textContentType={mode === "sign-in" ? "password" : "newPassword"}
					editable={!submitting}
					style={s.input}
				/>

				{error ? (
					<View accessibilityRole="alert" style={s.error}>
						<Feather name="alert-triangle" size={14} color={t.red} />
						<Text style={s.errorText}>{error}</Text>
					</View>
				) : null}

				<Pressable
					accessibilityRole="button"
					disabled={!canSubmit}
					onPress={() => {
						haptics.tap();
						void submit();
					}}
					style={[s.submit, !canSubmit && s.submitDisabled]}
				>
					{submitting ? (
						<ActivityIndicator color={t.onAccent} />
					) : (
						<Text style={s.submitText}>{mode === "sign-in" ? "Sign in" : "Create account"}</Text>
					)}
				</Pressable>

				<Pressable accessibilityRole="button" onPress={onClose} disabled={submitting}>
					<Text style={s.later}>Cancel</Text>
				</Pressable>
			</View>
		</SheetScreen>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		row: { flexDirection: "row", gap: 8 },
		chip: {
			paddingVertical: 7,
			paddingHorizontal: 12,
			borderRadius: 8,
			borderWidth: 1,
			borderColor: t.borderDefault,
		},
		chipText: { color: t.textSecondary, fontSize: 13, fontWeight: "500" },
		input: {
			borderWidth: 1,
			borderColor: t.borderDefault,
			borderRadius: 8,
			paddingHorizontal: 12,
			paddingVertical: 10,
			color: t.textPrimary,
			fontSize: 15,
		},
		error: { flexDirection: "row", alignItems: "center", gap: 8 },
		errorText: { color: t.red, fontSize: 13, flex: 1 },
		submit: {
			backgroundColor: t.blue,
			borderRadius: 8,
			paddingVertical: 12,
			alignItems: "center",
		},
		submitDisabled: { opacity: 0.4 },
		submitText: { color: t.onAccent, fontSize: 15, fontWeight: "600" },
		later: { color: t.textTertiary, fontSize: 14, textAlign: "center", paddingVertical: 4 },
	});
