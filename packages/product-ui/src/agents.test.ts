import { describe, expect, it } from "vitest";
import { AGENT_OPTIONS, agentLabel, getAgentIdentity } from "./agents";

describe("Reasonix identity", () => {
	it("is available in agent choices with its product label and logo key", () => {
		expect(AGENT_OPTIONS).toContain("reasonix");
		expect(agentLabel("reasonix")).toBe("Reasonix");
		expect(getAgentIdentity("reasonix")).toEqual({ id: "reasonix", label: "Reasonix", logoKey: "reasonix", initial: "R" });
	});
});
