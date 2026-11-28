// One CSS-pixel header height for the macOS shell and native button centerline.
export const MAC_TITLEBAR_HEIGHT = 36;
// Red button's left edge lines up with the sidebar row icons (1px edge + 8px rail + 10px row inset).
export const MAC_WINDOW_BUTTON_X = 19;
export const MAC_WINDOW_BUTTON_RADIUS = 7;
// The framed center panel sits this far below the window top (inset + 1px
// border), so its header centres lower than a bare 36px row would.
export const MAC_CENTER_PANEL_TOP_OFFSET = 7;
// Traffic-light row: centred on the framed panel's header (y = 25 at 100%).
export const MAC_TRAFFIC_LIGHT_ROW_HEIGHT = MAC_TITLEBAR_HEIGHT + 2 * MAC_CENTER_PANEL_TOP_OFFSET;
// Interface v2 (Developer mode preview) sits everything 2px further from the
// window edges: the panel inset, the sidebar's edge and so the native buttons,
// which move 2px right. The top bar keeps its height (its controls shrink
// instead), so the buttons' row and centre line stay put.
export const MAC_INTERFACE_V2_EDGE_EXTRA = 2;
