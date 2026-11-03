import { describe, expect, it } from "vitest";
import { shareLinkErrorMessage } from "../components/OpenShareLinkDialog";
import { CloudCpError } from "./cloud-cp/errors";

const t = (key: string) => key;

describe("shareLinkErrorMessage", () => {
	it("says a link stopped working rather than that it was not found", () => {
		expect(shareLinkErrorMessage(new CloudCpError("not found", { status: 404 }), t)).toBe("shareLink.expired");
	});
	it("says a link is for someone else rather than that access to an organization is missing", () => {
		expect(shareLinkErrorMessage(new CloudCpError("You do not have access to this organization.", { status: 403 }), t)).toBe("shareLink.notForYou");
	});
	it("passes any other failure through", () => {
		expect(shareLinkErrorMessage(new Error("offline"), t)).toBe("offline");
	});
});
