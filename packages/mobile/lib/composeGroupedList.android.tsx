import { Column, ListItem, Text } from "@expo/ui/jetpack-compose";
import { alpha, clickable, clip, combinedClickable, fillMaxWidth, padding, Shapes, testID as testTag } from "@expo/ui/jetpack-compose/modifiers";
import { Children, cloneElement, isValidElement, type ReactElement, type ReactNode } from "react";
import { useTheme } from "./ThemeProvider";
import { gutter, radius, space, stateOpacity } from "./tokens";

// Android: a Material 3 settings list, built from Compose ListItems in the
// theme's surfaces. Rows in a section join into one rounded group, fully
// rounded at its ends and slightly between rows, the "connected list" shape
// @expo/ui's FieldGroup draws, but in the app's palette rather than the
// wallpaper's.

/** The padding a LazyColumn of GroupedSections takes, inset to the screen's gutter. */
export const groupedListPadding = { start: gutter, end: gutter, top: space.sm, bottom: space.xxl };

type RowPosition = "only" | "leading" | "middle" | "trailing";

const FULL = radius.xl;
const JOINED = radius.xs;

function cornerRadii(position: RowPosition) {
	const top = position === "only" || position === "leading" ? FULL : JOINED;
	const bottom = position === "only" || position === "trailing" ? FULL : JOINED;
	return { topStart: top, topEnd: top, bottomStart: bottom, bottomEnd: bottom };
}

function rowPosition(index: number, total: number): RowPosition {
	if (total === 1) return "only";
	if (index === 0) return "leading";
	return index === total - 1 ? "trailing" : "middle";
}

/** A titled group of GroupedRows, with an optional footnote under it. */
export function GroupedSection({ title, footer, footerColor, children }: {
	title?: string;
	footer?: string;
	/** The footnote's colour, when it reports a problem. */
	footerColor?: string;
	children: ReactNode;
}) {
	const t = useTheme();
	const rows = Children.toArray(children).filter(isValidElement) as ReactElement<{ position?: RowPosition }>[];
	return (
		<Column verticalArrangement={{ spacedBy: space.hair }} modifiers={[fillMaxWidth()]}>
			{title ? (
				<Text color={t.textSecondary} style={{ typography: "titleSmall" }} modifiers={[padding(gutter, space.sm, gutter, space.xs)]}>{title}</Text>
			) : null}
			{rows.map((row, index) => cloneElement(row, { position: rowPosition(index, rows.length) }))}
			{footer ? (
				<Text color={footerColor ?? t.textSecondary} style={{ typography: "bodySmall" }} modifiers={[padding(gutter, space.xs, gutter, space.none)]}>{footer}</Text>
			) : null}
		</Column>
	);
}

/** One row of a GroupedSection. A row with `onPress` is clickable across its width and ripples. */
export function GroupedRow({ headline, supporting, leading, trailing, onPress, onLongPress, disabled = false, destructive = false, testID, position = "only" }: {
	headline: string;
	supporting?: string;
	leading?: ReactNode;
	trailing?: ReactNode;
	onPress?: () => void;
	/** A long press too, as a settings row that copies its value answers one. */
	onLongPress?: () => void;
	/** Dims the row and stops it taking taps, for an action that can't run right now. */
	disabled?: boolean;
	/** Draws the headline in the error colour, for an action that removes something. */
	destructive?: boolean;
	testID?: string;
	/** Set by GroupedSection. */
	position?: RowPosition;
}) {
	const t = useTheme();
	const press = disabled ? [] : onLongPress ? [combinedClickable({ onClick: onPress, onLongClick: onLongPress })] : onPress ? [clickable(onPress)] : [];
	return (
		<ListItem
			colors={{
				containerColor: t.bgElevated,
				contentColor: destructive ? t.red : t.textPrimary,
				supportingContentColor: t.textSecondary,
				leadingContentColor: t.textSecondary,
				trailingContentColor: t.textSecondary,
			}}
			modifiers={[
				fillMaxWidth(),
				// Clipped before it is made clickable, so the ripple keeps the row's shape.
				clip(Shapes.RoundedCorner(cornerRadii(position))),
				...press,
				...(disabled ? [alpha(stateOpacity.disabled)] : []),
				...(testID ? [testTag(testID)] : []),
			]}
		>
			<ListItem.HeadlineContent><Text>{headline}</Text></ListItem.HeadlineContent>
			{supporting ? <ListItem.SupportingContent><Text>{supporting}</Text></ListItem.SupportingContent> : null}
			{leading ? <ListItem.LeadingContent>{leading}</ListItem.LeadingContent> : null}
			{trailing != null ? (
				<ListItem.TrailingContent>{typeof trailing === "string" ? <Text>{trailing}</Text> : trailing}</ListItem.TrailingContent>
			) : null}
		</ListItem>
	);
}
