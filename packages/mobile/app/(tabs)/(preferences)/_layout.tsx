import { boardTitle } from "../../../lib/board-tabs";
import { TabStack } from "../../../lib/TabStack";

/**
 * Android's Settings tab. iOS pushes Settings as a page from the board's bar
 * instead. Its own pages (Machines, Appearance, a machine) still open on the
 * root stack, over the tabs, so back returns here.
 */
export default function PreferencesTabLayout() {
	return <TabStack screen="preferences" title={boardTitle("(preferences)")} />;
}
