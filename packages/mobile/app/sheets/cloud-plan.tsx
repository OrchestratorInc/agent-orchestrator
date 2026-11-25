import { useLocalSearchParams } from "expo-router";
import { CloudPlanSheet } from "../../lib/cloud/CloudPlanSheet";

/** The organization's plan, and why it refused a request when one did (lib/cloud/planRefusal). */
export default function CloudPlanRoute() {
	const { code, message, resetsAt } = useLocalSearchParams<{ code?: string; message?: string; resetsAt?: string }>();
	return <CloudPlanSheet code={code} message={message} resetsAt={resetsAt} />;
}

export { SheetErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";
