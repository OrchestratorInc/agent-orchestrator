import type { ImageSourcePropType } from "react-native";
import { symbols } from "./symbols";
import type { MaterialDrawableName, ToolbarIcon, ToolbarSymbol } from "./toolbarIcon.types";

export type { MaterialDrawableName, ToolbarIcon, ToolbarSymbol, ToolbarSymbolName } from "./toolbarIcon.types";

// Bundled, not resolved by name: a `require` of a missing file fails at bundle
// time rather than drawing an empty button, so this map and assets/icons must
// agree exactly, and the type keeps it complete. It is the only place a
// drawable is required; every other module asks `materialDrawable`.
const drawables: Record<MaterialDrawableName, ImageSourcePropType> = {
	account_circle: require("../assets/icons/account_circle.xml"),
	add: require("../assets/icons/add.xml"),
	add_comment: require("../assets/icons/add_comment.xml"),
	archive: require("../assets/icons/archive.xml"),
	arrow_back_ios_new: require("../assets/icons/arrow_back_ios_new.xml"),
	arrow_downward: require("../assets/icons/arrow_downward.xml"),
	arrow_drop_down: require("../assets/icons/arrow_drop_down.xml"),
	arrow_forward: require("../assets/icons/arrow_forward.xml"),
	arrow_upward: require("../assets/icons/arrow_upward.xml"),
	attach_file: require("../assets/icons/attach_file.xml"),
	bolt: require("../assets/icons/bolt.xml"),
	bug_report: require("../assets/icons/bug_report.xml"),
	build: require("../assets/icons/build.xml"),
	chat_bubble: require("../assets/icons/chat_bubble.xml"),
	check: require("../assets/icons/check.xml"),
	check_circle: require("../assets/icons/check_circle.xml"),
	chevron_right: require("../assets/icons/chevron_right.xml"),
	close: require("../assets/icons/close.xml"),
	cloud: require("../assets/icons/cloud.xml"),
	code: require("../assets/icons/code.xml"),
	compress: require("../assets/icons/compress.xml"),
	content_copy: require("../assets/icons/content_copy.xml"),
	contrast: require("../assets/icons/contrast.xml"),
	data_usage: require("../assets/icons/data_usage.xml"),
	delete: require("../assets/icons/delete.xml"),
	description: require("../assets/icons/description.xml"),
	desktop_windows: require("../assets/icons/desktop_windows.xml"),
	developer_board: require("../assets/icons/developer_board.xml"),
	devices: require("../assets/icons/devices.xml"),
	difference: require("../assets/icons/difference.xml"),
	dns: require("../assets/icons/dns.xml"),
	draft: require("../assets/icons/draft.xml"),
	edit: require("../assets/icons/edit.xml"),
	expand_less: require("../assets/icons/expand_less.xml"),
	expand_more: require("../assets/icons/expand_more.xml"),
	filter_list: require("../assets/icons/filter_list.xml"),
	find_in_page: require("../assets/icons/find_in_page.xml"),
	folder: require("../assets/icons/folder.xml"),
	group: require("../assets/icons/group.xml"),
	history: require("../assets/icons/history.xml"),
	host: require("../assets/icons/host.xml"),
	keep: require("../assets/icons/keep.xml"),
	keep_off: require("../assets/icons/keep_off.xml"),
	language: require("../assets/icons/language.xml"),
	laptop: require("../assets/icons/laptop.xml"),
	lightbulb: require("../assets/icons/lightbulb.xml"),
	logout: require("../assets/icons/logout.xml"),
	mic: require("../assets/icons/mic.xml"),
	more_horiz: require("../assets/icons/more_horiz.xml"),
	more_vert: require("../assets/icons/more_vert.xml"),
	network_check: require("../assets/icons/network_check.xml"),
	notifications: require("../assets/icons/notifications.xml"),
	open_in_new: require("../assets/icons/open_in_new.xml"),
	palette: require("../assets/icons/palette.xml"),
	photo: require("../assets/icons/photo.xml"),
	play_arrow: require("../assets/icons/play_arrow.xml"),
	power_settings_new: require("../assets/icons/power_settings_new.xml"),
	qr_code_scanner: require("../assets/icons/qr_code_scanner.xml"),
	rate_review: require("../assets/icons/rate_review.xml"),
	refresh: require("../assets/icons/refresh.xml"),
	search: require("../assets/icons/search.xml"),
	send: require("../assets/icons/send.xml"),
	sensors: require("../assets/icons/sensors.xml"),
	settings: require("../assets/icons/settings.xml"),
	share: require("../assets/icons/share.xml"),
	smartphone: require("../assets/icons/smartphone.xml"),
	speed: require("../assets/icons/speed.xml"),
	stacks: require("../assets/icons/stacks.xml"),
	stop: require("../assets/icons/stop.xml"),
	system_update: require("../assets/icons/system_update.xml"),
	tab: require("../assets/icons/tab.xml"),
	terminal: require("../assets/icons/terminal.xml"),
	tune: require("../assets/icons/tune.xml"),
	unfold_more: require("../assets/icons/unfold_more.xml"),
	verified_user: require("../assets/icons/verified_user.xml"),
	vertical_align_top: require("../assets/icons/vertical_align_top.xml"),
	visibility: require("../assets/icons/visibility.xml"),
	visibility_off: require("../assets/icons/visibility_off.xml"),
	wrap_text: require("../assets/icons/wrap_text.xml"),
};

/** A bundled Material Symbol, for Compose `Icon` and anything else that takes an image source. */
export function materialDrawable(name: MaterialDrawableName): ImageSourcePropType {
	return drawables[name];
}

/**
 * The `icon` for a native header item (`Stack.Toolbar.Button`, `.Menu`). On
 * Android those render as Compose icon buttons, which draw only an image source.
 */
export function toolbarIcon(symbol: ToolbarSymbol): ToolbarIcon {
	return drawables[typeof symbol === "string" ? symbols[symbol].android : symbol.android];
}
