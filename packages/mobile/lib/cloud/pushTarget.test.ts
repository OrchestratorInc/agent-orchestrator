import { expect, it } from "vitest";
import { cloudPushTarget } from "./pushTarget";

it("routes Orchestrator Cloud notifications to their session, project, or the inbox, and leaves machine pushes alone", () => {
	expect(cloudPushTarget({ source: "cloud", sessionId: "s1", projectId: "p" })).toEqual({ pathname: "/session/[id]", params: { id: "s1", hostId: "orchestrator-cloud" } });
	expect(cloudPushTarget({ source: "cloud", projectId: "p" })).toEqual({ pathname: "/project/[id]", params: { id: "p", hostId: "orchestrator-cloud" } });
	expect(cloudPushTarget({ source: "cloud" })).toBe("/notifications");
	expect(cloudPushTarget({ sessionId: "s1" })).toBeNull();
});
