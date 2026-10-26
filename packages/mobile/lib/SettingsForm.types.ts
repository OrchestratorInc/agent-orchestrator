import type { ReactNode } from "react";
import { symbols, type IOSSymbol } from "./symbols";
import type { Theme } from "./theme";
import type { MaterialDrawableName } from "./toolbarIcon.types";

/**
 * The glyph at the start of each settings row, as SF Symbol and Material
 * Symbol. Android draws each from the shared drawable registry
 * (materialDrawable), so a row can only name a glyph listed here, and typecheck
 * holds every Material name to one the registry bundles.
 */
export const settingsIcons = {
	defaultMachine: symbols.desktop,
	status: { ios: "dot.radiowaves.left.and.right", android: "sensors" },
	testConnection: { ios: "bolt.horizontal.circle", android: "network_check" },
	scan: symbols.qrScanner,
	addMachine: symbols.add,
	retry: symbols.refresh,
	environments: symbols.server,
	account: symbols.account,
	signOut: symbols.signOut,
	layoutGrid: { ios: "grid", android: "grid_on" },
	appearance: { ios: "circle.lefthalf.filled", android: "contrast" },
	theme: symbols.palette,
	notifications: symbols.notifications,
	update: symbols.update,
	report: { ios: "ladybug", android: "bug_report" },
	feature: { ios: "lightbulb", android: "lightbulb" },
} as const satisfies Record<string, { ios: IOSSymbol; android: MaterialDrawableName }>;

export type SettingsIconName = keyof typeof settingsIcons;

/** What a row's value says about its state. The words carry it; the colour backs them up. */
export type SettingsTone = "default" | "good" | "bad";

export function settingsToneColor(t: Theme, tone: SettingsTone | undefined): string {
	return tone === "good" ? t.green : tone === "bad" ? t.red : t.textSecondary;
}

export type SettingsFormProps = { children?: ReactNode; testID?: string };

export type SettingsSectionProps = {
	title?: string;
	/** Explains the section, under its rows. */
	footer?: string;
	children?: ReactNode;
	testID?: string;
};

type RowBase = {
	icon: SettingsIconName;
	label: string;
	testID?: string;
};

/** Opens another page. iOS adds the disclosure chevron; Material lists don't. */
export type SettingsLinkRowProps = RowBase & {
	/** The current setting, shown beside the label (under it on Android). */
	value?: string;
	onPress(): void;
};

/** A label and a value, with nothing to tap. */
export type SettingsValueRowProps = RowBase & {
	value?: string;
	tone?: SettingsTone;
	/** Shows the platform's spinner in place of the value. */
	loading?: boolean;
};

/** An action that runs in place: a native button row. */
export type SettingsButtonRowProps = RowBase & {
	value?: string;
	tone?: SettingsTone;
	/** Removes or signs out of something: drawn in red, with the destructive role on iOS. */
	destructive?: boolean;
	disabled?: boolean;
	/** Shows the platform's spinner and holds the row until it clears. */
	loading?: boolean;
	onPress(): void;
};

export type SettingsToggleRowProps = RowBase & {
	value: boolean;
	disabled?: boolean;
	onValueChange(next: boolean): void;
};

export type SettingsOption<T extends string> = { value: T; label: string };

export type SettingsPickerRowProps<T extends string> = RowBase & {
	options: readonly SettingsOption<T>[];
	selectedValue: T;
	onValueChange(next: T): void;
	disabled?: boolean;
	/**
	 * "segmented" lays a short choice out as Material's segmented buttons on
	 * Android, its single-select case. iOS keeps the menu picker either way.
	 */
	appearance?: "menu" | "segmented";
};

export type SettingsChoiceOption<T extends string> = SettingsOption<T> & {
	/** A native preview drawn beside the label, such as a theme swatch. */
	accessory?: ReactNode;
};

/**
 * A whole section that picks one of a list: iOS's inline picker with its
 * checkmark, Android's radio-button list.
 */
export type SettingsChoiceSectionProps<T extends string> = {
	/** Names the choice for screen readers; the page title already shows it. */
	label: string;
	options: readonly SettingsChoiceOption<T>[];
	selectedValue: T;
	onValueChange(next: T): void;
	footer?: string;
	testID?: string;
};
