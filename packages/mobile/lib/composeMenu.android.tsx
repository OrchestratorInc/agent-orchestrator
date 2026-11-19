import { DropdownMenuItem, Icon, Text } from "@expo/ui/jetpack-compose";
import { width as widthModifier } from "@expo/ui/jetpack-compose/modifiers";
import { useEffect, type ReactNode } from "react";
import { BackHandler } from "react-native";
import { logoFor } from "./harnessLogoAssets";
import { useTheme, useThemeState } from "./ThemeProvider";
import { materialDrawable } from "./toolbarIcon.android";
import { font, iconSize } from "./tokens";

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
