import type { ImageSourcePropType } from "react-native";
import type { SettingsIconName } from "./SettingsForm.types";
import { materialDrawable } from "./toolbarIcon.android";

// Compose `Icon` draws an image source, not a glyph name, so each settings
// glyph is bundled. A glyph the shared drawables already bundle comes from
// there; the rest are required here, each file named for its glyph's Android
// name in settingsIcons. A `require` of a missing one fails at bundle time
// instead of drawing an empty slot, and the Record type keeps the list complete.
const drawables: Record<SettingsIconName, ImageSourcePropType> = {
	defaultMachine: require("../assets/icons/desktop_windows.xml"),
	status: require("../assets/icons/sensors.xml"),
	testConnection: require("../assets/icons/network_check.xml"),
	scan: require("../assets/icons/qr_code_scanner.xml"),
	addMachine: materialDrawable("add"),
	retry: require("../assets/icons/refresh.xml"),
	environments: require("../assets/icons/dns.xml"),
	account: require("../assets/icons/account_circle.xml"),
	signOut: require("../assets/icons/logout.xml"),
	layoutGrid: require("../assets/icons/grid_on.xml"),
	appearance: require("../assets/icons/contrast.xml"),
	theme: require("../assets/icons/palette.xml"),
	notifications: materialDrawable("notifications"),
	update: require("../assets/icons/system_update.xml"),
	report: require("../assets/icons/bug_report.xml"),
	feature: require("../assets/icons/lightbulb.xml"),
};

export function settingsDrawable(name: SettingsIconName): ImageSourcePropType {
	return drawables[name];
}

/** The open-menu cue at the end of a picker row, and the mark on its chosen item. */
export const pickerDrawables = {
	dropDown: require("../assets/icons/arrow_drop_down.xml") as ImageSourcePropType,
	check: materialDrawable("check"),
};
