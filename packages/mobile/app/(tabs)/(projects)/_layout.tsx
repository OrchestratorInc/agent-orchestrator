import { boardTitle } from "../../../lib/board-tabs";
import { TabStack } from "../../../lib/TabStack";

export default function ProjectsTabLayout() {
	return <TabStack screen="projects" title={boardTitle("(projects)")} />;
}
