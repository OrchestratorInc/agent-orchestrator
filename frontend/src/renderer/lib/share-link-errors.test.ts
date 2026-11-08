import { describe, expect, it } from "vitest";
import { shareLinkErrorMessage } from "../components/OpenShareLinkDialog";
import { appI18n } from "../i18n";
import { CloudCpError } from "./cloud-cp/errors";

const t = (key: string) => key;
const CODE = { code: "k7Hq2x9PmB4wR5tY8uI0oP" };
const TOKEN = { orgId: "3f2a1b4c-5d6e-4f70-8a9b-0c1d2e3f4a5b", token: "tok" };

describe("shareLinkErrorMessage", () => {
	it("says a link stopped working rather than that it was not found", () => {
		expect(shareLinkErrorMessage(new CloudCpError("not found", { status: 404 }), t)).toBe("shareLink.expired");
		expect(shareLinkErrorMessage(new CloudCpError("not found", { status: 404, code: "not_found" }), t, CODE)).toBe("shareLink.expired");
		expect(shareLinkErrorMessage(new CloudCpError("not found", { status: 404 }), t, TOKEN)).toBe("shareLink.expired");
	});
	it("says a link is for someone else rather than that access to an organization is missing", () => {
		expect(shareLinkErrorMessage(new CloudCpError("You do not have access to this organization.", { status: 403 }), t)).toBe("shareLink.notForYou");
		expect(shareLinkErrorMessage(new CloudCpError("forbidden", { status: 403, code: "forbidden" }), t, CODE)).toBe("shareLink.notForYou");
		expect(shareLinkErrorMessage(new CloudCpError("forbidden", { status: 403 }), t, TOKEN)).toBe("shareLink.notForYou");
	});
	it("asks for a new link when the control plane can't read a short link's code", () => {
		// A control plane from before codes rejects the unknown field; one with codes rejects a malformed code.
		expect(shareLinkErrorMessage(new CloudCpError("The request body is invalid.", { status: 400, code: "invalid_request" }), t, CODE)).toBe("shareLink.unavailable");
		expect(shareLinkErrorMessage(new CloudCpError("The share code is malformed.", { status: 400, code: "invalid_request" }), t, CODE)).toBe("shareLink.unavailable");
		expect(shareLinkErrorMessage(new CloudCpError("The request body is invalid.", { status: 400 }), appI18n.t.bind(appI18n), CODE))
			.toBe("This link can't be opened right now. Ask for a new link.");
	});
	it("keeps a link from before codes showing the control plane's own bad-request text", () => {
		expect(shareLinkErrorMessage(new CloudCpError("The request body is invalid.", { status: 400 }), t, TOKEN)).toBe("The request body is invalid.");
		expect(shareLinkErrorMessage(new CloudCpError("The request body is invalid.", { status: 400 }), t)).toBe("The request body is invalid.");
	});
	it("passes any other failure through", () => {
		expect(shareLinkErrorMessage(new Error("offline"), t)).toBe("offline");
		expect(shareLinkErrorMessage(new Error("offline"), t, CODE)).toBe("offline");
		expect(shareLinkErrorMessage(new CloudCpError("boom", { status: 500 }), t, CODE)).toBe("boom");
	});
});
