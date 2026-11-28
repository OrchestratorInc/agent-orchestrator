import { Box, Button, DropdownMenu, DropdownMenuItem, Host, Icon, RNHostView, Row, Text } from "@expo/ui/jetpack-compose";
import { align, fillMaxWidth, height, onSizeChanged, padding, semantics, testID, weight, width as widthModifier, type ModifierConfig } from "@expo/ui/jetpack-compose/modifiers";
import { createContext, useContext, useEffect, useState, type ReactElement, type ReactNode } from "react";
import { BackHandler, PixelRatio, StyleSheet } from "react-native";
import { COMPOSER_DISC } from "./chat/composerStyles";
import { logoFor } from "./harnessLogoAssets";
import { useTheme, useThemeState } from "./ThemeProvider";
import { materialDrawable } from "./toolbarIcon.android";
import { font, iconSize, space, touchTarget } from "./tokens";

// A focusable popup takes the window's focus from the prompt, and Android puts
// the keyboard away; the menus leave it with the field, so it stays up.
export const KEEP_KEYBOARD = { focusable: false } as const;

/**
 * A submenu's popup, which a second tap on the row that opened it closes. A
 * submenu that dismissed itself on an outside click closed on that tap's down,
 * and the row's click then opened it again. Its parent menu still hears an
 * outside tap and closes everything.
 *
 * Only for a submenu that opens clear of its row: Compose places a tall one
 * low in its parent over the row instead, and there an outside tap is the only
 * way back to the parent, so it keeps KEEP_KEYBOARD.
 */
export const SUBMENU = { ...KEEP_KEYBOARD, dismissOnClickOutside: false } as const;

/** A menu that does not take focus never hears back, so the screen closes it. */
export function useCloseMenuOnBack(open: boolean, close: () => void) {
	useEffect(() => {
		if (!open) return;
		const subscription = BackHandler.addEventListener("hardwareBackPress", () => { close(); return true; });
		return () => subscription.remove();
	}, [open]);
}

/**
 * One choice in a Compose dropdown, ticked when it is the current one. A menu
 * is as wide as its widest row, so a `width` on its rows sets the menu's.
 */
export function MenuRow({ label, leading, trailing, selected = false, width, onClick }: {
	label: string;
	leading?: ReactNode;
	/** Drawn at the row's end in place of the tick, such as a submenu's chevron. */
	trailing?: ReactNode;
	selected?: boolean;
	width?: number;
	onClick: () => void;
}) {
	const t = useTheme();
	return (
		<DropdownMenuItem onClick={onClick} elementColors={{ textColor: t.textPrimary }} modifiers={width ? [widthModifier(width)] : undefined}>
			<DropdownMenuItem.Text>
				<Text maxLines={2} overflow="ellipsis" style={{ typography: "bodyLarge", fontFamily: font.regular }}>{label}</Text>
			</DropdownMenuItem.Text>
			{leading ? <DropdownMenuItem.LeadingIcon>{leading}</DropdownMenuItem.LeadingIcon> : null}
			{trailing ? <DropdownMenuItem.TrailingIcon>{trailing}</DropdownMenuItem.TrailingIcon> : selected ? (
				<DropdownMenuItem.TrailingIcon>
					<Icon source={materialDrawable("check")} size={iconSize.xl} tint={t.accent} contentDescription="Selected" />
				</DropdownMenuItem.TrailingIcon>
			) : null}
		</DropdownMenuItem>
	);
}

const MARK_SIZE = iconSize.lg;

/** A harness's brand mark in its own colours, or the terminal symbol when it has none. */
export function HarnessMark({ harness }: { harness: string }) {
	const t = useTheme();
	const { scheme } = useThemeState();
	// The menu follows the app's colour scheme, so the mark drawn for that scheme
	// stays visible on it: white marks such as opencode's ship a dark twin.
	const source = logoFor(harness, scheme);
	if (!source) return <Icon source={materialDrawable("terminal")} size={MARK_SIZE} tint={t.textSecondary} />;
	return <Icon source={source} size={MARK_SIZE} tint={null} />;
}

/**
 * A composer's controls row, the new task's and the chat's: the model's
 * capsule at the start with the effort's speedometer beside it, and the
 * approval mode at the end. A Compose row measures its children in order and
 * gave the capsule all the width a long model name asked for, pushing the
 * speedometer and the approval mode out of the row; here the capsule is
 * measured last, with what they leave (see ModelCapsuleMenu).
 */
export function ComposerControlsRow({ capsule, effort, end }: {
	/** The model's capsule: a ModelCapsuleMenu. */
	capsule: ReactNode;
	/** The effort's speedometer, a React Native control. */
	effort?: ReactElement | null;
	/** The approval mode's menu. */
	end?: ReactNode;
}) {
	const { scheme } = useThemeState();
	// The width the capsule and the speedometer share. It does not depend on the
	// name, so whether the name fits cannot flip back and forth.
	const [room, setRoom] = useState<number | null>(null);
	const nameRoom = room === null ? null : room - (effort ? touchTarget : 0);
	return (
		// The composer's slot starts its child rather than stretching it, and a
		// Host sized only to its height would take no width there at all.
		<Host matchContents={{ vertical: true }} colorScheme={scheme} style={rowStyles.host}>
			<Row verticalAlignment="center" modifiers={[fillMaxWidth(), height(touchTarget)]}>
				<Row verticalAlignment="center" modifiers={[weight(1), onSizeChanged(({ width }) => setRoom(width))]}>
					<CapsuleRoom.Provider value={nameRoom}>{capsule}</CapsuleRoom.Provider>
					{effort ? <RNHostView matchContents>{effort}</RNHostView> : null}
				</Row>
				{end}
			</Row>
		</Host>
	);
}

/** The width the row leaves the capsule, in dp; null until the row is measured. */
const CapsuleRoom = createContext<number | null>(null);

/**
 * The model's capsule and the menu anchored to it. It takes the width its
 * name needs and no more than the row leaves it: weighted without filling, it
 * is measured after the speedometer and the approval mode, and a long name
 * ellipsizes instead of pushing them out.
 */
export function ModelCapsuleMenu({ expanded, onDismiss, children, ...face }: CapsuleFaceProps & {
	expanded: boolean;
	onDismiss: () => void;
	/** The menu's items. */
	children: ReactNode;
}) {
	const t = useTheme();
	return (
		<DropdownMenu expanded={expanded} properties={KEEP_KEYBOARD} onDismissRequest={onDismiss} color={t.bgElevated} modifiers={[weight(1, { fill: false })]}>
			<DropdownMenu.Trigger>
				<ModelCapsuleButton {...face} />
			</DropdownMenu.Trigger>
			<DropdownMenu.Items>{children}</DropdownMenu.Items>
		</DropdownMenu>
	);
}

type CapsuleFaceProps = {
	/** The agent whose mark leads the capsule; none draws the name alone. */
	harness?: string;
	title: string;
	onClick: () => void;
	enabled: boolean;
	testTag: string;
	/** What TalkBack hears: the capsule shows the model alone. */
	contentDescription: string;
};

/**
 * The capsule's face: the harness's mark, the model's name and the menu
 * arrow, filled in the card colour, the composer's discs' height. The arrow is
 * drawn over the end of the name's box, which keeps room for it, rather than
 * after the name, where a Compose row would leave it no width once a long name
 * ellipsized.
 */
function ModelCapsuleButton({ harness, title, onClick, enabled, testTag, contentDescription }: CapsuleFaceProps) {
	const t = useTheme();
	const room = useContext(CapsuleRoom);
	const mark = harness ? <HarnessMark harness={harness} /> : null;
	const arrow = (modifiers?: ModifierConfig[]) => <Icon source={materialDrawable("unfold_more")} size={iconSize.md} tint={t.textTertiary} modifiers={modifiers} />;
	// A row too narrow for a few letters of the name (the largest display and
	// font sizes) draws the mark and the arrow alone rather than a cut-off
	// ellipsis; TalkBack still hears the name.
	const showsName = !mark || room === null || room - capsuleChrome(Boolean(mark)) >= minNameWidth();
	return (
		<Button
			onClick={onClick}
			colors={{ containerColor: t.bgElevated, contentColor: t.textPrimary }}
			// Material pads a button 8dp above and below. In the disc's height
			// that left the name less than its line, and a larger font cut it off.
			contentPadding={{ start: space.md, end: space.xs, top: 0, bottom: 0 }}
			enabled={enabled}
			modifiers={[height(COMPOSER_DISC), testID(testTag), semantics({ contentDescription })]}
		>
			{showsName ? (
				<Box contentAlignment="centerStart">
					<Row verticalAlignment="center">
						{mark}
						<Text maxLines={1} overflow="ellipsis" style={{ typography: "labelLarge", fontFamily: font.medium }} modifiers={[padding(mark ? space.sm : 0, 0, NAME_END, 0)]}>
							{title}
						</Text>
					</Row>
					{arrow([align("centerEnd")])}
				</Box>
			) : (
				<Row verticalAlignment="center">
					{mark}
					{arrow([padding(space.xxs, 0, 0, 0)])}
				</Row>
			)}
		</Button>
	);
}

// The name's box keeps this much room at its end for the arrow drawn over it.
const NAME_END = space.xxs + iconSize.md;

/** The capsule's width without its name, in dp. */
function capsuleChrome(hasMark: boolean): number {
	return space.md + (hasMark ? MARK_SIZE + space.sm : 0) + NAME_END + space.xs;
}

/** Room for about two letters and the ellipsis of the capsule's label type (14sp), in dp. */
function minNameWidth(): number {
	return 14 * PixelRatio.getFontScale() * 2;
}

const rowStyles = StyleSheet.create({
	host: { alignSelf: "stretch" },
});
