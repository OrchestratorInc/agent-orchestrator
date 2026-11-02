import * as Clipboard from "expo-clipboard";
import { useCallback, useState, type ReactNode } from "react";
import { ActionSheetIOS, Alert, Modal, Platform, Pressable, StyleSheet, Text, View } from "react-native";
import { deleteArtifact, getArtifactText } from "./api";
import { artifactMenuActions, looksBinary, type ArtifactMenuAction, type SessionArtifact } from "./artifacts";
import { userFacingError } from "./connectionError";
import { haptics } from "./haptics";
import { Feather, type FeatherIconName } from "./icons";
import { useApp } from "./store";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { space, type } from "./tokens";

const LABELS: Record<ArtifactMenuAction, string> = {
	open: "Open",
	ask: "Ask the agent",
	copy: "Copy contents",
	history: "Version history",
	delete: "Delete artifact",
};

const GLYPHS: Record<ArtifactMenuAction, FeatherIconName> = {
	open: "eye",
	ask: "message-square-plus",
	copy: "copy",
	history: "clock",
	delete: "trash-2",
};

// Lets the menu's close animation finish before an action raises an alert.
const AFTER_CLOSE_MS = 180;

type OpenMenu = { artifact: SessionArtifact; actions: ArtifactMenuAction[]; run: (action: ArtifactMenuAction) => void };

/**
 * Open, Copy, History and Delete for one session's artifacts, shared by the gallery's
 * long-press menu and the viewer's header menu. iOS shows a native action
 * sheet. Android shows `menu`, an in-app list like the worker row's options:
 * an Android alert holds at most three buttons. Callers render `menu`.
 * `onDeleted` runs once the daemon has removed the file.
 */
export function useArtifactActions(sessionId: string, onDeleted?: (artifact: SessionArtifact) => void) {
	const { config, refresh } = useApp();
	const [open, setOpen] = useState<OpenMenu | null>(null);

	const copy = useCallback(async (artifact: SessionArtifact) => {
		if (!config) return;
		try {
			const text = await getArtifactText(config, sessionId, artifact.path);
			if (looksBinary(text)) throw new Error("This file isn't text, so it can't be copied.");
			await Clipboard.setStringAsync(text);
			haptics.success();
		} catch (cause) {
			haptics.error();
			Alert.alert("Couldn't copy the artifact", userFacingError(cause));
		}
	}, [config, sessionId]);

	const confirmDelete = useCallback((artifact: SessionArtifact) => {
		haptics.warning();
		Alert.alert("Delete artifact?", `This permanently deletes ${artifact.name} from this session's artifacts folder.`, [
			{ text: "Cancel", style: "cancel" },
			{
				text: "Delete",
				style: "destructive",
				onPress: () => {
					if (!config) return;
					void deleteArtifact(config, sessionId, artifact.path)
						.then(() => {
							haptics.success();
							onDeleted?.(artifact);
							void refresh().catch(() => {});
						})
						.catch((cause) => {
							haptics.error();
							Alert.alert("Couldn't delete the artifact", userFacingError(cause));
						});
				},
			},
		]);
	}, [config, onDeleted, refresh, sessionId]);

	/** Opens the menu for `artifact`; Open runs `onOpen`, Ask the agent `onAsk`, Version history `onHistory`. */
	const openMenu = useCallback((artifact: SessionArtifact, { inViewer = false, onOpen, onAsk, onHistory }: { inViewer?: boolean; onOpen?: () => void; onAsk?: () => void; onHistory?: () => void } = {}) => {
		const actions = artifactMenuActions(artifact, { inViewer });
		if (!actions.length) return;
		haptics.select();
		const run = (action: ArtifactMenuAction) => {
			if (action === "open") onOpen?.();
			else if (action === "ask") onAsk?.();
			else if (action === "copy") void copy(artifact);
			else if (action === "history") onHistory?.();
			else confirmDelete(artifact);
		};
		if (Platform.OS === "ios") {
			const destructiveButtonIndex = actions.indexOf("delete");
			ActionSheetIOS.showActionSheetWithOptions(
				{
					title: artifact.name,
					options: [...actions.map((action) => LABELS[action]), "Cancel"],
					cancelButtonIndex: actions.length,
					destructiveButtonIndex: destructiveButtonIndex >= 0 ? destructiveButtonIndex : undefined,
				},
				(index) => {
					const action = actions[index];
					if (action) run(action);
				},
			);
			return;
		}
		setOpen({ artifact, actions, run });
	}, [confirmDelete, copy]);

	const close = useCallback(() => setOpen(null), []);
	const menu: ReactNode = open ? <ArtifactActionModal menu={open} onClose={close} /> : null;

	return { copy, confirmDelete, openMenu, menu };
}

/** Android's artifact menu, styled like the worker row's options. */
function ArtifactActionModal({ menu, onClose }: { menu: OpenMenu; onClose(): void }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const choose = (action: ArtifactMenuAction) => {
		haptics.tap();
		onClose();
		setTimeout(() => menu.run(action), AFTER_CLOSE_MS);
	};
	return (
		<Modal transparent visible animationType="fade" onRequestClose={onClose}>
			<View style={styles.scrim}>
				<Pressable accessibilityLabel="Dismiss artifact actions" onPress={onClose} style={StyleSheet.absoluteFill} />
				<View accessibilityViewIsModal style={styles.menu}>
					<Text numberOfLines={2} style={styles.title}>{menu.artifact.name}</Text>
					<View style={styles.list}>
						{menu.actions.map((action) => {
							const destructive = action === "delete";
							return (
								<Pressable
									key={action}
									accessibilityRole="button"
									accessibilityLabel={LABELS[action]}
									onPress={() => choose(action)}
									android_ripple={{ color: destructive ? t.tintRed : t.accentTint }}
									style={({ pressed }) => [styles.row, pressed && styles.pressed]}
								>
									<Feather name={GLYPHS[action]} size={18} color={destructive ? t.red : t.textSecondary} />
									<Text style={[styles.rowText, destructive && styles.destructive]}>{LABELS[action]}</Text>
								</Pressable>
							);
						})}
					</View>
					<Pressable
						accessibilityRole="button"
						accessibilityLabel="Cancel artifact actions"
						onPress={onClose}
						style={({ pressed }) => [styles.cancel, pressed && styles.pressed]}
					>
						<Text style={styles.cancelText}>Cancel</Text>
					</Pressable>
				</View>
			</View>
		</Modal>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		scrim: { flex: 1, alignItems: "center", justifyContent: "center", padding: space.xxl, backgroundColor: "rgba(0, 0, 0, 0.58)" },
		menu: { width: "100%", maxWidth: 340, padding: space.xl, gap: space.sm, borderRadius: 20, borderWidth: StyleSheet.hairlineWidth, borderColor: t.borderDefault, backgroundColor: t.bgElevated, elevation: 12 },
		title: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.title3.fontSize, lineHeight: type.title3.lineHeight, fontWeight: "600" },
		list: { marginTop: space.xs, marginHorizontal: -8 },
		row: { minHeight: 48, paddingHorizontal: space.sm, flexDirection: "row", alignItems: "center", gap: space.md, borderRadius: 8 },
		rowText: { fontFamily: "Geist_500Medium", color: t.textPrimary, fontSize: type.callout.fontSize, lineHeight: type.callout.lineHeight, fontWeight: "500" },
		destructive: { color: t.red },
		cancel: { minHeight: 42, paddingHorizontal: space.lg, borderRadius: 20, alignItems: "center", justifyContent: "center", marginTop: space.sm, alignSelf: "flex-end", backgroundColor: t.bgElevatedHover, borderWidth: StyleSheet.hairlineWidth, borderColor: t.borderDefault },
		cancelText: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight, fontWeight: "600" },
		pressed: { opacity: 0.76 },
	});
