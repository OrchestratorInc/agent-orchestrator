import { describe, expect, it } from "vitest";
import { parseShareDeepLink } from "./share-deeplink";

const ORG = "c49408fa-d160-4a2c-8564-021c2b0f0a48";
const LINK = "7b72301d-1111-4a2c-8564-021c2b0f0a48";
const SECRET = "a".repeat(40) + "-_Z";

describe("parseShareDeepLink", () => {
	it("parses a well-formed share link", () => {
		expect(parseShareDeepLink(`ao-app://share/${ORG}/${LINK}#${SECRET}`)).toEqual({
			orgId: ORG,
			linkId: LINK,
			secret: SECRET,
		});
	});

	it.each([
		["auth callback", `ao-app://callback?code=x&state=y`],
		["other scheme", `https://share/${ORG}/${LINK}#${SECRET}`],
		["other host", `ao-app://sharex/${ORG}/${LINK}#${SECRET}`],
		["missing secret", `ao-app://share/${ORG}/${LINK}`],
		["short secret", `ao-app://share/${ORG}/${LINK}#abc`],
		["secret with bad chars", `ao-app://share/${ORG}/${LINK}#${"a".repeat(42)}!`],
		["non-uuid org", `ao-app://share/not-an-org/${LINK}#${SECRET}`],
		["extra path segment", `ao-app://share/${ORG}/${LINK}/x#${SECRET}`],
		["query string", `ao-app://share/${ORG}/${LINK}?redeem=1#${SECRET}`],
		["credentials", `ao-app://user:pw@share/${ORG}/${LINK}#${SECRET}`],
		["garbage", "not a url"],
	])("rejects %s", (_label, raw) => {
		expect(parseShareDeepLink(raw)).toBeNull();
	});
});
