import type { BoardTabBarProps } from "./BoardTabBar.types";

export type { BoardTabBarProps } from "./BoardTabBar.types";

/**
 * iOS draws the board's tabs and its New task button in the system's own tab
 * bar (see app/(tabs)/_layout.tsx), so there is nothing to draw here.
 * Android's is ./BoardTabBar.android.
 */
export function BoardTabBar(_props: BoardTabBarProps): null {
	return null;
}
