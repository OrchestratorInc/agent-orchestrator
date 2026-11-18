import type { CSSProperties } from "react";
import { AGENT_FRAME_EDGE_PX } from "../../shared/browser-agent-frame";

/**
 * The frame around a tab the agent works in. The view host insets that tab's
 * page by the same edge (shared/browser-agent-frame.ts), so this paints beside
 * the page, never over it: the person's clicks reach the page and the agent's
 * screenshots stay clean. A Working-blue edge marks the tab; while the agent
 * acts in it, the edge glows and breathes.
 *
 * Decorative only: the tab's accessible name and the panel's status region
 * already say whose tab it is and what the agent is doing.
 */
export function BrowserAgentFrame({ acting }: { acting: boolean }) {
	return (
		<div
			aria-hidden="true"
			className="browser-agent-frame"
			data-acting={acting ? "true" : undefined}
			data-testid="browser-agent-frame"
			style={{ "--agent-frame-edge": `${AGENT_FRAME_EDGE_PX}px` } as CSSProperties}
		>
			<div className="browser-agent-frame__ground" />
			<div className="browser-agent-frame__glow" />
		</div>
	);
}
