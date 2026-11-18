import { describe, expect, it } from "vitest";
import { AGENT_FRAME_EDGE_PX, AGENT_FRAME_STRIP_PX, insetForAgentFrame } from "./browser-agent-frame";

describe("insetForAgentFrame", () => {
	it("leaves an edge on three sides and the strip along the bottom", () => {
		expect(insetForAgentFrame({ x: 10, y: 20, width: 320, height: 240 })).toEqual({
			x: 10 + AGENT_FRAME_EDGE_PX,
			y: 20 + AGENT_FRAME_EDGE_PX,
			width: 320 - AGENT_FRAME_EDGE_PX * 2,
			height: 240 - AGENT_FRAME_EDGE_PX - AGENT_FRAME_STRIP_PX,
		});
	});

	it("scales the frame with the page zoom", () => {
		expect(insetForAgentFrame({ x: 0, y: 0, width: 400, height: 300 }, 2)).toEqual({
			x: AGENT_FRAME_EDGE_PX * 2,
			y: AGENT_FRAME_EDGE_PX * 2,
			width: 400 - AGENT_FRAME_EDGE_PX * 4,
			height: 300 - (AGENT_FRAME_EDGE_PX + AGENT_FRAME_STRIP_PX) * 2,
		});
	});

	it("leaves a slot too small to frame as it is", () => {
		const tiny = { x: 0, y: 0, width: 4, height: 20 };
		expect(insetForAgentFrame(tiny)).toBe(tiny);
	});
});
