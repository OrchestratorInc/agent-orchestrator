import { TabPage } from "../../../lib/TabPage";
import SettingsScreen from "../../settings/index";

// The Settings list itself, as a tab; see the group's layout.
export default function PreferencesTab() {
	return <TabPage><SettingsScreen /></TabPage>;
}

export { RouteErrorBoundary as ErrorBoundary } from "../../../lib/RouteErrorBoundary";
