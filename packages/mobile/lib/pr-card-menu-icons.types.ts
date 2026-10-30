import type { MenuAction } from "@expo/ui/community/menu";

/** The leading glyph of each action in a pull-request row's long-press menu. */
export type PRCardMenuIcons = Record<"openInGitHub" | "copyLink", MenuAction["image"]>;
