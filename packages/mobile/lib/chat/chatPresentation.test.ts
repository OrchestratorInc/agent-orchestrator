import { describe, expect, it } from "vitest";

import { requestPresentation } from "./chatPresentation";

describe("chat presentation", () => {
	it("distinguishes pending requests from historical resolved requests", () => {
		expect(requestPresentation("approval", true)).toEqual({ icon: "shield", title: "Approval needed", badge: "Needs approval", tone: "attention" });
		expect(requestPresentation("input", true)).toEqual({ icon: "message-circle", title: "Agent needs input", badge: "Needs input", tone: "accent" });
		expect(requestPresentation("approval", false)).toEqual({ icon: "check-circle", title: "Approval resolved", badge: "Resolved", tone: "muted" });
		expect(requestPresentation("input", false)).toEqual({ icon: "check-circle", title: "Input resolved", badge: "Resolved", tone: "muted" });
	});
});
