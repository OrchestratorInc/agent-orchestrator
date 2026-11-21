import { memo } from "react";
import { Platform, Pressable, StyleSheet, Text, View } from "react-native";
import { AppSymbol } from "../AppSymbol";
import { rowDividerWidth } from "../divider";
import { usePressFeedback } from "../pressFeedback";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { font, fontScaleCap, gutter, iconSize, space, touchTarget, type } from "../tokens";
import type { WorkspaceFileStatus } from "./api";
import { FILE_ICON_SLOT, FileTypeIcon, FolderTypeIcon, LineCounts } from "./components";
import { FileRowMenu, type FileRowAction } from "./FileRowMenu";
import { changeAccessibilityLabel, plural, sectionLabel, splitPath, type ChangeEntry } from "./model";

/** Where a row's text starts, so the divider under it lines up with the names rather than the glyphs. */
const TEXT_INSET = gutter + FILE_ICON_SLOT + space.md;

/** iOS marks a row that opens a page with a chevron; Material's list rows carry none. */
function Disclosure() {
	const t = useTheme();
	if (Platform.OS !== "ios") return null;
	return <AppSymbol name="chevronForward" size={iconSize.sm} color={t.textFaint} weight="semibold" />;
}

export function FileRowDivider() {
	const styles = useThemedStyles(makeStyles);
	return <View style={styles.divider} />;
}

/** A changed file: its glyph and status, where it lives, its line counts and any queued comments. */
export const ChangeRow = memo(function ChangeRow({ file, comments, onPress, onAction }: {
	file: ChangeEntry;
	comments: number;
	onPress: (file: ChangeEntry) => void;
	onAction: (action: FileRowAction, file: ChangeEntry) => void;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const { name, dir } = splitPath(file.path);
	const badges = file.sections.filter((section) => section !== "unstaged" && section !== "committed").map((section) => sectionLabel[section]);
	const detail = [file.previousPath ? `from ${file.previousPath}` : dir, ...badges].filter(Boolean).join(" · ");
	// A deleted file has only its diff left to read.
	const actions: FileRowAction[] = file.status === "deleted" ? ["openDiff", "copyPath", "share"] : ["openDiff", "openFile", "copyPath", "share"];
	return (
		<FileRowMenu
			actions={actions}
			onAction={(action) => onAction(action, file)}
			onPress={() => onPress(file)}
			accessibilityLabel={`${changeAccessibilityLabel(file)}${comments ? `, ${plural(comments, "queued comment")}` : ""}`}
			accessibilityHint="Opens the diff"
			style={styles.fileRow}
		>
			<FileTypeIcon path={file.path} status={file.status} />
			<View style={styles.text}>
				<Text numberOfLines={1} maxFontSizeMultiplier={fontScaleCap.body} style={[styles.fileName, file.status === "deleted" && styles.fileNameDeleted]}>{name}</Text>
				{detail ? <Text numberOfLines={1} ellipsizeMode="head" maxFontSizeMultiplier={fontScaleCap.body} style={styles.detail}>{detail}</Text> : null}
			</View>
			{comments ? (
				<View style={styles.commentCount}>
					<AppSymbol name="chat" size={iconSize.sm} color={t.textSecondary} />
					<Text maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.commentCountText}>{comments}</Text>
				</View>
			) : null}
			<LineCounts additions={file.additions} deletions={file.deletions} binary={file.binary} />
			<Disclosure />
		</FileRowMenu>
	);
});

/**
 * A file in a folder or a search result. A search result names its folder under
 * it, since the list mixes files from everywhere.
 */
export const PathRow = memo(function PathRow({ path, name, status, onPress, onAction }: {
	path: string;
	/** Given in a folder listing, where the folder is the page's title. */
	name?: string;
	status?: WorkspaceFileStatus;
	onPress: (path: string, changed: boolean) => void;
	onAction: (action: FileRowAction, path: string) => void;
}) {
	const styles = useThemedStyles(makeStyles);
	const parts = splitPath(path);
	const changed = Boolean(status && status !== "unmodified");
	const actions: FileRowAction[] = changed ? ["openDiff", "openFile", "copyPath", "share"] : ["openFile", "copyPath", "share"];
	return (
		<FileRowMenu
			actions={actions}
			onAction={(action) => onAction(action, path)}
			onPress={() => onPress(path, changed)}
			accessibilityLabel={`${name ?? parts.name}${!name && parts.dir ? ` in ${parts.dir}` : ""}${changed ? `, ${status}` : ""}`}
			accessibilityHint={changed ? "Opens the diff" : "Opens the file"}
			style={styles.entry}
		>
			<FileTypeIcon path={path} status={changed ? status : undefined} />
			<View style={styles.text}>
				<Text maxFontSizeMultiplier={fontScaleCap.body} numberOfLines={1} style={styles.entryName}>{name ?? parts.name}</Text>
				{!name && parts.dir ? <Text maxFontSizeMultiplier={fontScaleCap.body} numberOfLines={1} ellipsizeMode="head" style={styles.detail}>{parts.dir}</Text> : null}
			</View>
			<Disclosure />
		</FileRowMenu>
	);
});

/** A folder: opens a page of its own, so the system back goes up one level. */
export const FolderRow = memo(function FolderRow({ path, name, hasChanges, onPress }: {
	path: string;
	name: string;
	hasChanges?: boolean;
	onPress: (path: string) => void;
}) {
	const styles = useThemedStyles(makeStyles);
	const feedback = usePressFeedback("row");
	return (
		<Pressable
			accessibilityRole="button"
			accessibilityLabel={`Folder ${name}${hasChanges ? ", has changes" : ""}`}
			onPress={() => onPress(path)}
			android_ripple={feedback.android_ripple}
			style={({ pressed }) => [styles.entry, pressed && feedback.pressed]}
		>
			<FolderTypeIcon name={name} />
			<Text maxFontSizeMultiplier={fontScaleCap.body} numberOfLines={1} style={[styles.text, styles.entryName]}>{name}</Text>
			{hasChanges ? <View style={styles.changeDot} /> : null}
			<Disclosure />
		</Pressable>
	);
});

const makeStyles = (t: Theme) => StyleSheet.create({
	fileRow: { minHeight: touchTarget + space.sm, flexDirection: "row", alignItems: "center", gap: space.md, paddingHorizontal: gutter, paddingVertical: space.sm },
	entry: { minHeight: touchTarget + space.xxs, flexDirection: "row", alignItems: "center", gap: space.md, paddingHorizontal: gutter, paddingVertical: space.xs },
	text: { flex: 1, minWidth: 0 },
	fileName: { ...type.subheadline, fontFamily: font.medium, color: t.textPrimary },
	fileNameDeleted: { color: t.textSecondary, textDecorationLine: "line-through" },
	entryName: { ...type.subheadline, color: t.textPrimary },
	detail: { ...type.footnote, color: t.textTertiary },
	commentCount: { flexDirection: "row", alignItems: "center", gap: space.hair },
	commentCountText: { ...type.footnote, fontFamily: font.medium, color: t.textSecondary },
	changeDot: { width: 7, height: 7, borderRadius: 4, backgroundColor: t.amber },
	divider: { height: rowDividerWidth, backgroundColor: t.borderSubtle, marginLeft: TEXT_INSET },
});
