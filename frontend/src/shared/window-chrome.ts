// One CSS-pixel header height for the macOS shell and native button centerline.
export const MAC_TITLEBAR_HEIGHT = 36;
// Red button's left edge lines up with the sidebar row icons (8px rail + 10px row inset).
export const MAC_WINDOW_BUTTON_X = 18;
export const MAC_WINDOW_BUTTON_RADIUS = 7;
// The framed center panel sits this far below the window top (inset + 1px
// border), so its header centres lower than a bare 36px row would.
export const MAC_CENTER_PANEL_TOP_OFFSET = 7;
// Traffic-light row: centred on the framed panel's header (y = 25 at 100%).
export const MAC_TRAFFIC_LIGHT_ROW_HEIGHT = MAC_TITLEBAR_HEIGHT + 2 * MAC_CENTER_PANEL_TOP_OFFSET;
