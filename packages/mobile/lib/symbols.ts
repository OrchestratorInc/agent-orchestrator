import type { AndroidSymbol } from "expo-symbols";
import type { AccessibilityProps } from "react-native";
// SF Symbols 4.2 is the set iOS 16.4 ships, and 16.4 is the deployment target.
// A name added in a later release resolves to nothing on an older device and
// draws a blank square, so the registry is held to this set at compile time.
import type { SFSymbols4_2 } from "sf-symbols-typescript";

/** An SF Symbol every supported iOS version can draw. */
export type IOSSymbol = SFSymbols4_2;

/**
 * One glyph, named for each platform: iOS draws the SF Symbol and Android the
 * Material Symbol, so each side shows its own system's icon for the same job.
 */
export type PlatformSymbol = { ios: IOSSymbol; android: AndroidSymbol };

/**
 * The app's chrome glyphs: headers, toolbars, menus, settings rows, tabs and
 * status. Lucide (`./icons`) stays for domain glyphs with no system symbol (PR,
 * merge) and for the timeline's activity glyphs.
 *
 * Names are by job, not by drawing, so a call site reads as what the control
 * does. The values also suit APIs that take a bare name: `ios` for a SwiftUI
 * `systemImage`, a `Stack.Toolbar` icon or a `NativeTabs` `sf`, and `android`
 * for a `NativeTabs` `md`.
 *
 * A new Android name also needs its font codepoint in `./symbolCodepoints`,
 * which typecheck asks for.
 */
export const symbols = {
	// Navigation and header actions
	settings: { ios: "gearshape", android: "settings" },
	notifications: { ios: "bell", android: "notifications" },
	add: { ios: "plus", android: "add" },
	// A header overflow. M3 stacks it vertically in a top app bar.
	more: { ios: "ellipsis.circle", android: "more_vert" },
	// An overflow inside a row or card, where a vertical glyph reads as a grip.
	moreHorizontal: { ios: "ellipsis", android: "more_horiz" },
	close: { ios: "xmark", android: "close" },
	back: { ios: "chevron.backward", android: "arrow_back" },
	search: { ios: "magnifyingglass", android: "search" },
	filter: { ios: "line.3.horizontal.decrease.circle", android: "filter_list" },

	// Disclosure. M3 discloses with expand_more / expand_less.
	chevronUp: { ios: "chevron.up", android: "expand_less" },
	chevronDown: { ios: "chevron.down", android: "expand_more" },
	chevronForward: { ios: "chevron.forward", android: "chevron_right" },
	chevronBackward: { ios: "chevron.backward", android: "chevron_left" },
	chevronUpDown: { ios: "chevron.up.chevron.down", android: "unfold_more" },

	// Item actions
	delete: { ios: "trash", android: "delete" },
	archive: { ios: "archivebox", android: "archive" },
	pin: { ios: "pin", android: "keep" },
	unpin: { ios: "pin.slash", android: "keep_off" },
	rename: { ios: "pencil", android: "edit" },
	copy: { ios: "doc.on.doc", android: "content_copy" },
	share: { ios: "square.and.arrow.up", android: "share" },
	openExternal: { ios: "arrow.up.right.square", android: "open_in_new" },
	refresh: { ios: "arrow.clockwise", android: "refresh" },
	stop: { ios: "stop.fill", android: "stop" },

	// Destinations and content
	workers: { ios: "square.3.layers.3d", android: "stacks" },
	projects: { ios: "folder", android: "folder" },
	folder: { ios: "folder", android: "folder" },
	file: { ios: "doc", android: "draft" },
	fileText: { ios: "doc.text", android: "description" },
	fileSearch: { ios: "doc.text.magnifyingglass", android: "find_in_page" },
	globe: { ios: "globe", android: "language" },
	chat: { ios: "bubble.left", android: "chat_bubble" },
	terminal: { ios: "terminal", android: "terminal" },

	// Controls
	qrScanner: { ios: "qrcode.viewfinder", android: "qr_code_scanner" },
	checkmark: { ios: "checkmark", android: "check" },
	checkCircle: { ios: "checkmark.circle", android: "check_circle" },
	wrapLines: { ios: "arrow.turn.down.left", android: "wrap_text" },
	zoomIn: { ios: "plus.magnifyingglass", android: "zoom_in" },
	zoomOut: { ios: "minus.magnifyingglass", android: "zoom_out" },
	textLarger: { ios: "textformat.size.larger", android: "text_increase" },
	textSmaller: { ios: "textformat.size.smaller", android: "text_decrease" },

	// Status
	info: { ios: "info.circle", android: "info" },
	warning: { ios: "exclamationmark.triangle", android: "warning" },
	error: { ios: "exclamationmark.circle", android: "error" },
	offline: { ios: "wifi.slash", android: "wifi_off" },
	history: { ios: "clock", android: "history" },
	timer: { ios: "timer", android: "timer" },

	// Empty states and machines
	inbox: { ios: "tray", android: "inbox" },
	desktop: { ios: "desktopcomputer", android: "desktop_windows" },
	devices: { ios: "laptopcomputer.and.iphone", android: "devices" },
	server: { ios: "server.rack", android: "dns" },
	cloud: { ios: "cloud", android: "cloud" },
	laptop: { ios: "laptopcomputer", android: "laptop" },
	// A small-form-factor machine. Material has no mini PC, so a board stands in.
	miniPc: { ios: "macmini", android: "developer_board" },
	workstation: { ios: "macstudio", android: "host" },
	// A machine the phone cannot reach: the plug, pulled.
	disconnected: { ios: "powerplug", android: "power_off" },
	idle: { ios: "moon.zzz", android: "bedtime" },
	diff: { ios: "plus.forwardslash.minus", android: "difference" },

	// Composer
	mic: { ios: "mic", android: "mic" },
	micOff: { ios: "mic.slash", android: "mic_off" },
	attach: { ios: "paperclip", android: "attach_file" },
	photo: { ios: "photo", android: "photo" },
	send: { ios: "arrow.up", android: "arrow_upward" },
	keyboardDismiss: { ios: "keyboard.chevron.compact.down", android: "keyboard_hide" },

	// Settings rows
	account: { ios: "person.crop.circle", android: "account_circle" },
	palette: { ios: "paintpalette", android: "palette" },
	update: { ios: "arrow.down.circle", android: "system_update" },
	help: { ios: "questionmark.circle", android: "help" },
	signOut: { ios: "rectangle.portrait.and.arrow.right", android: "logout" },
} as const satisfies Record<string, PlatformSymbol>;

export type AppSymbolName = keyof typeof symbols;

export function resolveSymbol(name: AppSymbolName | PlatformSymbol): PlatformSymbol {
	return typeof name === "string" ? symbols[name] : name;
}

/**
 * A glyph is decoration unless it is the only thing that names its control, so
 * by default screen readers skip it and read the control's own label. With a
 * label it is announced as an image under that label.
 */
export function symbolAccessibilityProps(label: string | undefined): AccessibilityProps {
	if (label) return { accessible: true, accessibilityRole: "image", accessibilityLabel: label };
	return { accessible: false, accessibilityElementsHidden: true, importantForAccessibility: "no-hide-descendants" };
}
