import type { TriageOrigin } from "../../../shared/triage";
import { TriageLauncher } from "./TriageLauncher";

export function TriageReportButton({ origin, className }: { origin?: TriageOrigin; className?: string }) {
	return <div className={className}><TriageLauncher origin={origin} /></div>;
}
