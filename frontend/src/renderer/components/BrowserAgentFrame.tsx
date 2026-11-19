import type { CSSProperties } from "react";
import { AGENT_FRAME_EDGE_PX } from "../../shared/browser-agent-frame";

/**
 * The frame around a tab the agent works in. The view host insets that tab's
 * page by the same edge (shared/browser-agent-frame.ts), so this paints beside
 * the page, never over it: the person's clicks reach the page and the agent's
 * screenshots stay clean. A faint Working-blue edge marks the tab. When the
 * agent starts working after a quiet spell (`greeting` counts up), a streak of
 * light runs around the edge as the mascot pops up, then fades away.
 *
 * Decorative only: the tab's accessible name and the panel's status region
 * already say whose tab it is and what the agent is doing.
 */
export function BrowserAgentFrame({ greeting }: { greeting: number }) {
	return (
		<div
			aria-hidden="true"
			className="browser-agent-frame"
			data-testid="browser-agent-frame"
			style={{ "--agent-frame-edge": `${AGENT_FRAME_EDGE_PX}px` } as CSSProperties}
		>
			<div className="browser-agent-frame__ground" />
			<div className="browser-agent-frame__edge" />
			{greeting > 0 ? (
				// Keyed so each greeting plays from the start.
				<div className="browser-agent-frame__sweep" data-testid="browser-agent-frame-sweep" key={greeting} />
			) : null}
		</div>
	);
}
