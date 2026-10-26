import type { ImageSourcePropType } from "react-native";
import type { MachineSymbol } from "./machineAppearance";

// Android's menus and Compose icons take an image source, not a glyph name, so
// each machine shape is bundled as its Material Symbol (the same names as
// machineSymbolNames resolves to). A missing file fails at bundle time.
const drawables: Record<MachineSymbol, ImageSourcePropType> = {
	server: require("../assets/icons/dns.xml"),
	cloud: require("../assets/icons/cloud.xml"),
	linux: require("../assets/icons/terminal.xml"),
	desktop: require("../assets/icons/desktop_windows.xml"),
	laptop: require("../assets/icons/laptop.xml"),
	"mac-mini": require("../assets/icons/developer_board.xml"),
	"mac-studio": require("../assets/icons/host.xml"),
};

/** A machine's Material Symbol drawable, for a MenuView action's image or a Compose `Icon`. */
export function machineMenuImage(symbol: MachineSymbol): ImageSourcePropType {
	return drawables[symbol];
}
