import { useEffect, useRef, useState } from "react";
import { ActivityIndicator, Pressable, StyleSheet, Text, TextInput, View } from "react-native";
import { Feather } from "../icons";
import type { Theme } from "../theme";
import { useThemedStyles } from "../ThemeProvider";
import { iconSize, space, type } from "../tokens";
import { displayBrowserUrl } from "./browserUrl";

export type BrowserToolbarProps = {
	url: string;
	title?: string;
	loading: boolean;
	canGoBack: boolean;
	canGoForward: boolean;
	canOpenExternal: boolean;
	onBack: () => void;
	onForward: () => void;
	onReload: () => void;
	onStop: () => void;
	onSubmitUrl: (value: string) => void;
	onCopy: () => void;
	onOpenExternal: () => void;
	onShare: () => void;
};

export function BrowserToolbar({
	url,
	title,
	loading,
	canGoBack,
	canGoForward,
	canOpenExternal,
	onBack,
	onForward,
	onReload,
	onStop,
	onSubmitUrl,
	onCopy,
	onOpenExternal,
	onShare,
}: BrowserToolbarProps) {
	const styles = useThemedStyles(makeStyles);
	const inputRef = useRef<TextInput>(null);
	const [editing, setEditing] = useState(false);
	const [draft, setDraft] = useState(url);
	useEffect(() => {
		if (!editing) setDraft(url);
	}, [editing, url]);
	useEffect(() => {
		if (!editing) return;
		const timer = setTimeout(() => inputRef.current?.focus(), 50);
		return () => clearTimeout(timer);
	}, [editing]);

	const submitDraft = () => {
		const next = draft.trim();
		setEditing(false);
		if (next) onSubmitUrl(next);
	};

	return (
		<View style={styles.toolbar}>
			<View style={styles.row}>
				<IconButton label="Back" icon="chevron-left" disabled={!canGoBack} onPress={onBack} />
				<IconButton label="Forward" icon="chevron-right" disabled={!canGoForward} onPress={onForward} />
				<IconButton label={loading ? "Stop loading" : "Reload"} icon={loading ? "x" : "refresh-cw"} disabled={!url && !loading} onPress={loading ? onStop : onReload} />
				{editing ? (
					<View style={styles.inputWrap}>
						<TextInput
							autoCapitalize="none"
							autoCorrect={false}
							clearButtonMode="while-editing"
							keyboardType="url"
							onBlur={() => setEditing(false)}
							onChangeText={setDraft}
							onSubmitEditing={submitDraft}
							placeholder="Enter URL"
							placeholderTextColor={styles.colors.textFaint}
							ref={inputRef}
							returnKeyType="go"
							selectTextOnFocus
							selectionColor={styles.colors.accent}
							style={styles.input}
							value={draft}
						/>
					</View>
				) : (
					<Pressable accessibilityRole="button" accessibilityLabel={url ? "Edit browser URL" : "Enter browser URL"} onPress={() => setEditing(true)} style={styles.location}>
						{loading ? <ActivityIndicator size="small" color={styles.colors.accent} style={styles.locationSpinner} /> : <Feather name="globe" size={iconSize.xs} color={styles.colors.textTertiary} />}
						<View style={styles.locationText}>
							<Text numberOfLines={1} style={styles.title}>{title || displayBrowserUrl(url) || "Enter a URL"}</Text>
							{url ? <Text numberOfLines={1} style={styles.url}>{displayBrowserUrl(url)}</Text> : null}
						</View>
					</Pressable>
				)}
			</View>
			<View style={styles.secondaryRow}>
				<SmallAction label="Copy" icon="copy" disabled={!url} onPress={onCopy} />
				<SmallAction label="Open" icon="external-link" disabled={!canOpenExternal} onPress={onOpenExternal} />
				<SmallAction label="Share" icon="link" disabled={!url} onPress={onShare} />
			</View>
		</View>
	);
}

function IconButton({ disabled, icon, label, onPress }: { disabled?: boolean; icon: string; label: string; onPress: () => void }) {
	const styles = useThemedStyles(makeStyles);
	return (
		<Pressable accessibilityRole="button" accessibilityLabel={label} accessibilityState={{ disabled }} disabled={disabled} hitSlop={8} onPress={onPress} style={[styles.iconButton, disabled && styles.disabled]}>
			<Feather name={icon} size={iconSize.md} color={disabled ? styles.colors.textFaint : styles.colors.textPrimary} />
		</Pressable>
	);
}

function SmallAction({ disabled, icon, label, onPress }: { disabled?: boolean; icon: string; label: string; onPress: () => void }) {
	const styles = useThemedStyles(makeStyles);
	return (
		<Pressable accessibilityRole="button" accessibilityLabel={`${label} current browser URL`} accessibilityState={{ disabled }} disabled={disabled} onPress={onPress} style={[styles.smallAction, disabled && styles.disabled]}>
			<Feather name={icon} size={iconSize.xs} color={disabled ? styles.colors.textFaint : styles.colors.textSecondary} />
			<Text style={[styles.smallActionText, disabled && { color: styles.colors.textFaint }]}>{label}</Text>
		</Pressable>
	);
}

const makeStyles = (t: Theme) => Object.assign(StyleSheet.create({
	toolbar: { borderTopWidth: 1, borderTopColor: t.borderDefault, backgroundColor: t.bgSurface, paddingHorizontal: space.md, paddingTop: space.sm, paddingBottom: space.md, gap: space.sm },
	row: { minHeight: 44, flexDirection: "row", alignItems: "center", gap: space.sm },
	secondaryRow: { flexDirection: "row", alignItems: "center", justifyContent: "flex-end", gap: space.sm },
	iconButton: { width: 36, height: 36, alignItems: "center", justifyContent: "center", borderRadius: 10, borderCurve: "continuous", backgroundColor: t.bgElevated },
	disabled: { opacity: 0.45 },
	location: { flex: 1, minHeight: 40, flexDirection: "row", alignItems: "center", gap: space.sm, borderRadius: 12, borderCurve: "continuous", backgroundColor: t.bgElevated, paddingHorizontal: space.md },
	locationSpinner: { width: iconSize.xs, height: iconSize.xs },
	locationText: { flex: 1, minWidth: 0 },
	title: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.caption1.fontSize, lineHeight: type.caption1.lineHeight, fontWeight: "600" },
	url: { fontFamily: t.fontMono, color: t.textTertiary, fontSize: type.caption2.fontSize, lineHeight: type.caption2.lineHeight },
	inputWrap: { flex: 1, minHeight: 40, justifyContent: "center", borderRadius: 12, borderCurve: "continuous", borderWidth: 1, borderColor: t.accentBorder, backgroundColor: t.bgElevated, paddingHorizontal: space.md },
	input: { minHeight: 38, padding: 0, fontFamily: t.fontMono, color: t.textPrimary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	smallAction: { minHeight: 30, flexDirection: "row", alignItems: "center", gap: space.xs, borderRadius: 8, borderCurve: "continuous", backgroundColor: t.bgElevated, paddingHorizontal: space.sm },
	smallActionText: { fontFamily: "Geist_500Medium", color: t.textSecondary, fontSize: type.caption2.fontSize, fontWeight: "500" },
}), { colors: t });
