import type { CSSProperties } from "react";
import { AGENT_FRAME_EDGE_PX, AGENT_FRAME_STRIP_PX } from "../../shared/browser-agent-frame";
import { AOMascot } from "./AOMascot";

/**
 * The frame around a tab the agent works in. The view host insets that tab's
 * page by the same amounts (shared/browser-agent-frame.ts), so everything
 * here paints beside the page, never over it: the person's clicks reach the
 * page and the agent's screenshots stay clean. A Working-blue edge marks the
 * tab; while the agent acts in it the edge glows and the mascot conducts.
 *
 * Decorative only: the tab's accessible name and the panel's status region
 * already say whose tab it is and what the agent is doing.
 */
export function BrowserAgentFrame({ acting, label }: { acting: boolean; label: string }) {
	return (
		<div
			aria-hidden="true"
			className="browser-agent-frame"
			data-acting={acting ? "true" : undefined}
			data-testid="browser-agent-frame"
			style={
				{
					"--agent-frame-edge": `${AGENT_FRAME_EDGE_PX}px`,
					"--agent-frame-strip": `${AGENT_FRAME_STRIP_PX}px`,
				} as CSSProperties
			}
		>
			<div className="browser-agent-frame__ground" />
			<div className="browser-agent-frame__glow" />
			<div className="browser-agent-frame__strip">
				<AOMascot className="browser-agent-frame__mascot" />
				<span className="browser-agent-frame__label" data-testid="browser-agent-frame-label">
					{label}
				</span>
			</div>
		</div>
	);
}
