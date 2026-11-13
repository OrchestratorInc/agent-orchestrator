import { expect, it } from "vitest";
import { cloudPushTarget, foreignCloudPushOrganization } from "./pushTarget";

it("routes Orchestrator Cloud notifications to their session, project, or the inbox, and leaves machine pushes alone", () => {
	expect(cloudPushTarget({ source: "cloud", sessionId: "s1", projectId: "p" })).toEqual({ pathname: "/session/[id]", params: { id: "s1", hostId: "orchestrator-cloud" } });
	expect(cloudPushTarget({ source: "cloud", projectId: "p" })).toEqual({ pathname: "/project/[id]", params: { id: "p", hostId: "orchestrator-cloud" } });
	expect(cloudPushTarget({ source: "cloud" })).toBe("/notifications");
	expect(cloudPushTarget({ sessionId: "s1" })).toBeNull();
});

// The phone works in one organization, as the desktop does; a session from
// another can't load through it.
it("names the organization a Cloud notification came from when it isn't the one the phone works in", () => {
	const organizations = [{ id: "a", displayName: "Acme" }, { id: "b", displayName: "Beta" }];
	expect(foreignCloudPushOrganization({ source: "cloud", orgId: "b" }, "a", organizations)).toBe("Beta");
	expect(foreignCloudPushOrganization({ source: "cloud", orgId: "c" }, "a", organizations)).toBe("another organization");
	expect(foreignCloudPushOrganization({ source: "cloud", orgId: "a" }, "a", organizations)).toBeUndefined();
	// Not known yet (a cold start before Cloud resolved), or not said: opened as before.
	expect(foreignCloudPushOrganization({ source: "cloud", orgId: "b" }, undefined, organizations)).toBeUndefined();
	expect(foreignCloudPushOrganization({ source: "cloud" }, "a", organizations)).toBeUndefined();
	expect(foreignCloudPushOrganization({ orgId: "b" }, "a", organizations)).toBeUndefined();
});
