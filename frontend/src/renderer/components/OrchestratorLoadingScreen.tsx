import { CenterPanelShell } from "./CenterPanelShell";

/** One stable surface across project creation, session startup, and UI loading. */
export function OrchestratorLoadingScreen({ framed = false }: { framed?: boolean }) {
	const content = (
		<div className="grid min-h-0 flex-1 place-items-center bg-background text-muted-foreground" aria-busy="true">
			<div role="status" className="chat-working-shimmer text-sm font-normal">
				Your project is being set up
			</div>
		</div>
	);
	return (
		<div className="absolute inset-0 z-20 flex flex-col" data-testid="orchestrator-loading-screen">
			{framed ? <CenterPanelShell>{content}</CenterPanelShell> : content}
		</div>
	);
}
