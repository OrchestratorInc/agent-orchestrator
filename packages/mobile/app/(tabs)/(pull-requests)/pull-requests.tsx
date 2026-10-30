import { TabPage } from "../../../lib/TabPage";
import PRsScreen from "../../prs/index";

// The Pull Requests page itself, as a tab: the same screen the board's bar and
// a notification open over the board, here with no machine or project chosen.
export default function PullRequestsTab() {
	return <TabPage><PRsScreen /></TabPage>;
}

export { RouteErrorBoundary as ErrorBoundary } from "../../../lib/RouteErrorBoundary";
