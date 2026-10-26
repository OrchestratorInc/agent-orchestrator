import type { ComponentProps } from "react";
// One import per glyph, from its own module. The package root re-exports every
// icon it ships, and a barrel is not tree-shaken here by default: a production
// export that imported the root carried 4,330 modules against 2,480 on main for
// the same screens. These are the only icons the app draws.
import Activity from "lucide-react-native/icons/activity";
import CircleAlert from "lucide-react-native/icons/circle-alert";
import TriangleAlert from "lucide-react-native/icons/triangle-alert";
import Archive from "lucide-react-native/icons/archive";
import Bell from "lucide-react-native/icons/bell";
import Check from "lucide-react-native/icons/check";
import CircleCheck from "lucide-react-native/icons/circle-check";
import Circle from "lucide-react-native/icons/circle";
import Clock from "lucide-react-native/icons/clock";
import Cloud from "lucide-react-native/icons/cloud";
import Code from "lucide-react-native/icons/code";
import Command from "lucide-react-native/icons/command";
import Copy from "lucide-react-native/icons/copy";
import CornerDownRight from "lucide-react-native/icons/corner-down-right";
import Delete from "lucide-react-native/icons/delete";
import Disc from "lucide-react-native/icons/disc";
import CloudDownload from "lucide-react-native/icons/cloud-download";
import PenLine from "lucide-react-native/icons/pen-line";
import Eye from "lucide-react-native/icons/eye";
import File from "lucide-react-native/icons/file";
import FileText from "lucide-react-native/icons/file-text";
import FileDiff from "lucide-react-native/icons/file-diff";
import ListTree from "lucide-react-native/icons/list-tree";
import Folder from "lucide-react-native/icons/folder";
import Flag from "lucide-react-native/icons/flag";
import GitBranch from "lucide-react-native/icons/git-branch";
import GitMerge from "lucide-react-native/icons/git-merge";
import GitPullRequest from "lucide-react-native/icons/git-pull-request";
import Globe from "lucide-react-native/icons/globe";
import Grid2x2 from "lucide-react-native/icons/grid-2x2";
import Image from "lucide-react-native/icons/image";
import Inbox from "lucide-react-native/icons/inbox";
import Info from "lucide-react-native/icons/info";
import Italic from "lucide-react-native/icons/italic";
import KeyRound from "lucide-react-native/icons/key-round";
import Laptop from "lucide-react-native/icons/laptop";
import Link from "lucide-react-native/icons/link";
import List from "lucide-react-native/icons/list";
import LoaderCircle from "lucide-react-native/icons/loader-circle";
import Map from "lucide-react-native/icons/map";
import Menu from "lucide-react-native/icons/menu";
import MessageCircle from "lucide-react-native/icons/message-circle";
import MessageSquare from "lucide-react-native/icons/message-square";
import MessageSquarePlus from "lucide-react-native/icons/message-square-plus";
import Mic from "lucide-react-native/icons/mic";
import CircleMinus from "lucide-react-native/icons/circle-minus";
import Monitor from "lucide-react-native/icons/monitor";
import MonitorCog from "lucide-react-native/icons/monitor-cog";
import MonitorOff from "lucide-react-native/icons/monitor-off";
import MonitorSmartphone from "lucide-react-native/icons/monitor-smartphone";
import RouteOff from "lucide-react-native/icons/route-off";
import Timer from "lucide-react-native/icons/timer";
import Unplug from "lucide-react-native/icons/unplug";
import Moon from "lucide-react-native/icons/moon";
import Palette from "lucide-react-native/icons/palette";
import Paperclip from "lucide-react-native/icons/paperclip";
import Pin from "lucide-react-native/icons/pin";
import Play from "lucide-react-native/icons/play";
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
import Square from "lucide-react-native/icons/square";
import Table from "lucide-react-native/icons/table";
import Terminal from "lucide-react-native/icons/terminal";
import Wrench from "lucide-react-native/icons/wrench";
import Trash from "lucide-react-native/icons/trash";
import Type from "lucide-react-native/icons/type";
import Underline from "lucide-react-native/icons/underline";
import User from "lucide-react-native/icons/user";
import WifiOff from "lucide-react-native/icons/wifi-off";
import X from "lucide-react-native/icons/x";
import OctagonX from "lucide-react-native/icons/octagon-x";
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
	"bell": Bell,
	"check": Check,
	"check-circle": CircleCheck,
	"circle": Circle,
	"clock": Clock,
	"cloud": Cloud,
	"code": Code,
	"command": Command,
	"copy": Copy,
	"corner-down-right": CornerDownRight,
	"delete": Delete,
	"disc": Disc,
	"download-cloud": CloudDownload,
	"edit-3": PenLine,
	"eye": Eye,
	"file": File,
	"file-text": FileText,
	"file-diff": FileDiff,
	"folder": Folder,
	"flag": Flag,
	"git-branch": GitBranch,
	"git-merge": GitMerge,
	"git-pull-request": GitPullRequest,
	"globe": Globe,
	"grid": Grid2x2,
	"image": Image,
	"inbox": Inbox,
	"info": Info,
	"italic": Italic,
	"key": KeyRound,
	"laptop": Laptop,
	"link": Link,
	"list": List,
	"list-tree": ListTree,
	"loader": LoaderCircle,
	"map": Map,
	"menu": Menu,
	"message-circle": MessageCircle,
	"message-square": MessageSquare,
	"message-square-plus": MessageSquarePlus,
	"mic": Mic,
	"minus-circle": CircleMinus,
	"monitor": Monitor,
	"monitor-cog": MonitorCog,
	"monitor-off": MonitorOff,
	"monitor-smartphone": MonitorSmartphone,
	"moon": Moon,
	"palette": Palette,
	"paperclip": Paperclip,
	"pin": Pin,
	"play": Play,
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
	"square": Square,
	"table": Table,
	"terminal": Terminal,
	"tool": Wrench,
	"timer": Timer,
	"trash": Trash,
	"type": Type,
	"underline": Underline,
	"unplug": Unplug,
	"user": User,
	"wifi-off": WifiOff,
	"x": X,
	"x-octagon": OctagonX,
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
