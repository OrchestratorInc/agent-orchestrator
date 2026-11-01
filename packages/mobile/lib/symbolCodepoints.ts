import type { AndroidSymbol } from "expo-symbols";
import type { symbols } from "./symbols";

type RegistryAndroidSymbol = (typeof symbols)[keyof typeof symbols]["android"];

/**
 * Where each registry glyph sits in the Material Symbols font, as listed in
 * expo-symbols' `build/android/symbols.json`. expo-symbols keeps that map
 * private, so `AppSymbol` carries its own copy for the glyphs it draws without
 * the library's view. The type asks for every registry name, so a new entry in
 * `./symbols` fails typecheck until its codepoint is added here, and a test
 * checks each value against the library's map.
 */
const codepoints: Record<RegistryAndroidSymbol, number> = {
	settings: 0xe8b8,
	notifications: 0xe7f4,
	add: 0xe145,
	more_vert: 0xe5d4,
	more_horiz: 0xe5d3,
	close: 0xe5cd,
	arrow_back: 0xe5c4,
	search: 0xe8b6,
	filter_list: 0xe152,
	expand_less: 0xe5ce,
	expand_more: 0xe5cf,
	chevron_right: 0xe5cc,
	chevron_left: 0xe5cb,
	unfold_more: 0xe5d7,
	delete: 0xe872,
	archive: 0xe149,
	keep: 0xe6aa,
	keep_off: 0xe6f9,
	edit: 0xe3c9,
	content_copy: 0xe14d,
	share: 0xe80d,
	open_in_new: 0xe89e,
	refresh: 0xe5d5,
	stop: 0xe047,
	stacks: 0xf500,
	folder: 0xe2c7,
	draft: 0xe66d,
	description: 0xe873,
	find_in_page: 0xe880,
	language: 0xe894,
	chat_bubble: 0xe0ca,
	terminal: 0xeb8e,
	qr_code_scanner: 0xf206,
	check: 0xe5ca,
	check_circle: 0xe86c,
	wrap_text: 0xe25b,
	zoom_in: 0xe8ff,
	zoom_out: 0xe900,
	text_increase: 0xeae2,
	text_decrease: 0xeadd,
	info: 0xe88e,
	warning: 0xe002,
	error: 0xe000,
	wifi_off: 0xe648,
	history: 0xe889,
	timer: 0xe425,
	inbox: 0xe156,
	desktop_windows: 0xe30c,
	devices: 0xe1b1,
	dns: 0xe875,
	cloud: 0xe2bd,
	laptop: 0xe31e,
	developer_board: 0xe30d,
	host: 0xf3d9,
	power_off: 0xe646,
	bedtime: 0xef44,
	difference: 0xeb7d,
	mic: 0xe029,
	mic_off: 0xe02b,
	open_in_full: 0xf1ce,
	close_fullscreen: 0xf1cf,
	attach_file: 0xe226,
	photo: 0xe410,
	arrow_upward: 0xe5d8,
	keyboard_hide: 0xe31a,
	account_circle: 0xe853,
	palette: 0xe40a,
	system_update: 0xe62a,
	help: 0xe887,
	logout: 0xe9ba,
};

/** The font character for a Material Symbol, if it is one the registry names. */
export function materialGlyph(symbol: AndroidSymbol): string | undefined {
	const codepoint = (codepoints as Partial<Record<AndroidSymbol, number>>)[symbol];
	return codepoint === undefined ? undefined : String.fromCharCode(codepoint);
}
