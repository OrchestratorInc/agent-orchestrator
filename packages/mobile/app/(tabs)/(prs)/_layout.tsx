import { boardTitle } from "../../../lib/board-tabs";
import { TabStack } from "../../../lib/TabStack";

export default function PullRequestsTabLayout() {
	return <TabStack screen="prs" title={boardTitle("(prs)")} />;
}
