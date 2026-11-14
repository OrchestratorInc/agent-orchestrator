import { boardTitle } from "../../../lib/board-tabs";
import { TabStack } from "../../../lib/TabStack";

export default function WorkersTabLayout() {
	return <TabStack screen="index" title={boardTitle("(workers)")} />;
}
