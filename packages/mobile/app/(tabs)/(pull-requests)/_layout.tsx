import { boardTitle } from "../../../lib/board-tabs";
import { TabStack } from "../../../lib/TabStack";

/** Android's Pull Requests tab. iOS opens the page as a sheet from the board's bar instead. */
export default function PullRequestsTabLayout() {
	return <TabStack screen="pull-requests" title={boardTitle("(pull-requests)")} />;
}
