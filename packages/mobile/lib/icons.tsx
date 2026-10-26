import type { ComponentProps } from "react";
// One import per glyph, from its own module. The package root re-exports every
// icon it ships, and a barrel is not tree-shaken here by default: a production
// export that imported the root carried 4,330 modules against 2,480 on main for
// the same screens. These are the only icons the app draws.
import Activity from "lucide-react-native/icons/activity";
import CircleAlert from "lucide-react-native/icons/circle-alert";
import TriangleAlert from "lucide-react-native/icons/triangle-alert";
import Archive from "lucide-react-native/icons/archive";
import Check from "lucide-react-native/icons/check";
import CircleCheck from "lucide-react-native/icons/circle-check";
import Circle from "lucide-react-native/icons/circle";
import Clock from "lucide-react-native/icons/clock";
import Copy from "lucide-react-native/icons/copy";
import CornerDownRight from "lucide-react-native/icons/corner-down-right";
import Delete from "lucide-react-native/icons/delete";
import Disc from "lucide-react-native/icons/disc";
import CloudDownload from "lucide-react-native/icons/cloud-download";
import PenLine from "lucide-react-native/icons/pen-line";
import File from "lucide-react-native/icons/file";
import FileText from "lucide-react-native/icons/file-text";
import FileDiff from "lucide-react-native/icons/file-diff";
import ListTree from "lucide-react-native/icons/list-tree";
import Folder from "lucide-react-native/icons/folder";
import GitBranch from "lucide-react-native/icons/git-branch";
import GitMerge from "lucide-react-native/icons/git-merge";
import GitPullRequest from "lucide-react-native/icons/git-pull-request";
import Globe from "lucide-react-native/icons/globe";
import KeyRound from "lucide-react-native/icons/key-round";
import Link from "lucide-react-native/icons/link";
import List from "lucide-react-native/icons/list";
import MessageCircle from "lucide-react-native/icons/message-circle";
import CircleMinus from "lucide-react-native/icons/circle-minus";
import Monitor from "lucide-react-native/icons/monitor";
import MonitorCog from "lucide-react-native/icons/monitor-cog";
import MonitorOff from "lucide-react-native/icons/monitor-off";
import MonitorSmartphone from "lucide-react-native/icons/monitor-smartphone";
import RouteOff from "lucide-react-native/icons/route-off";
import Timer from "lucide-react-native/icons/timer";
import Unplug from "lucide-react-native/icons/unplug";
import Moon from "lucide-react-native/icons/moon";
import Pin from "lucide-react-native/icons/pin";
import Plus from "lucide-react-native/icons/plus";
import Power from "lucide-react-native/icons/power";
import Radio from "lucide-react-native/icons/radio";
import RefreshCw from "lucide-react-native/icons/refresh-cw";
import Search from "lucide-react-native/icons/search";
import Send from "lucide-react-native/icons/send";
import Server from "lucide-react-native/icons/server";
import Settings from "lucide-react-native/icons/settings";
import Shield from "lucide-react-native/icons/shield";
import ShieldOff from "lucide-react-native/icons/shield-off";
import Shuffle from "lucide-react-native/icons/shuffle";
import Smartphone from "lucide-react-native/icons/smartphone";
import Terminal from "lucide-react-native/icons/terminal";
import Wrench from "lucide-react-native/icons/wrench";
import WifiOff from "lucide-react-native/icons/wifi-off";
import X from "lucide-react-native/icons/x";
import { useTheme } from "./ThemeProvider";

/**
 * The app's icons, drawn from the desktop's set.
 *
 * The renderer draws every icon from Lucide, and this is the same family at the
 * same version: a glyph the desktop shows is the glyph drawn here. Call sites
 * keep the names they already use, and the map records where Lucide renamed one.
 */
export const glyphs = {
	"activity": Activity,
	"alert-circle": CircleAlert,
	"alert-triangle": TriangleAlert,
	"archive": Archive,
	"check": Check,
	"check-circle": CircleCheck,
	"circle": Circle,
	"clock": Clock,
	"copy": Copy,
	"corner-down-right": CornerDownRight,
	"delete": Delete,
	"disc": Disc,
	"download-cloud": CloudDownload,
	"edit-3": PenLine,
	"file": File,
	"file-text": FileText,
	"file-diff": FileDiff,
	"folder": Folder,
	"git-branch": GitBranch,
	"git-merge": GitMerge,
	"git-pull-request": GitPullRequest,
	"globe": Globe,
	"key": KeyRound,
	"link": Link,
	"list": List,
	"list-tree": ListTree,
	"message-circle": MessageCircle,
	"minus-circle": CircleMinus,
	"monitor": Monitor,
	"monitor-cog": MonitorCog,
	"monitor-off": MonitorOff,
	"monitor-smartphone": MonitorSmartphone,
	"moon": Moon,
	"pin": Pin,
	"plus": Plus,
	"power": Power,
	"radio": Radio,
	"refresh-cw": RefreshCw,
	"route-off": RouteOff,
	"search": Search,
	"send": Send,
	"server": Server,
	"settings": Settings,
	"shield": Shield,
	"shield-off": ShieldOff,
	"shuffle": Shuffle,
	"smartphone": Smartphone,
	"terminal": Terminal,
	"tool": Wrench,
	"timer": Timer,
	"unplug": Unplug,
	"wifi-off": WifiOff,
	"x": X,
} as const;

export type FeatherIconName = keyof typeof glyphs;

/** Feather's calling convention, Lucide's drawings. */
function Icon({
	name,
	size = 24,
	color,
	style,
	...rest
}: {
	/**
	 * A known name autocompletes; a string is accepted because a few call sites
	 * carry an icon name out of data rather than a literal.
	 */
	name: FeatherIconName | (string & {}) | (number & {}) | (symbol & {});
	size?: number;
	color?: string;
	style?: ComponentProps<(typeof glyphs)[FeatherIconName]>["style"];
} & Omit<ComponentProps<(typeof glyphs)[FeatherIconName]>, "size" | "color" | "style">) {
	const t = useTheme();
	const Glyph = glyphs[name as FeatherIconName];
	if (!Glyph) return null;
	// Lucide's own default is `currentColor`, which react-native-svg resolves to
	// black: a call site that forgot a colour drew an invisible glyph on the dark
	// theme. The theme's secondary ink is what chrome glyphs use (see AppSymbol).
	return <Glyph size={size} color={color ?? t.textSecondary} style={style} strokeWidth={2} {...rest} />;
}

/**
 * `Feather.glyphMap` is only ever read as a type by call sites that take an icon
 * name in their props; this keeps those signatures working unchanged.
 */
export const Feather = Object.assign(Icon, { glyphMap: {} as Record<string, number> });
