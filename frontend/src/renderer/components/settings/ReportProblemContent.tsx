import { TriageLauncher } from "../triage/TriageLauncher";

export function ReportProblemContent({ active }: { active: boolean }) {
	return <TriageLauncher active={active} />;
}
