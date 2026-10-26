import type { ImageSourcePropType } from "react-native";
import type { MachineSymbol } from "./machineAppearance";

// A Compose menu item draws only an image source, so each machine shape is
// bundled as the Material Symbol nearest its SF Symbol in ./machine-menu-image.
// A `require` of a missing file fails at bundle time.
const drawables: Record<MachineSymbol, ImageSourcePropType> = {
	server: require("../assets/icons/dns.xml"),
	cloud: require("../assets/icons/cloud.xml"),
	linux: require("../assets/icons/terminal.xml"),
	desktop: require("../assets/icons/desktop_windows.xml"),
	laptop: require("../assets/icons/laptop.xml"),
	"mac-mini": require("../assets/icons/developer_board.xml"),
	"mac-studio": require("../assets/icons/host.xml"),
};

/** A machine's Material Symbol drawable, for a Compose `Icon`. */
export function machineDrawable(symbol: MachineSymbol): ImageSourcePropType {
	return drawables[symbol];
}
