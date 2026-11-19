import { describe, expect, it } from "vitest";
import { artifactViewerOrigins, parseSharedArtifactLink, sharedArtifactAppURL, sharedArtifactLinkId, sharedArtifactURL } from "../../../packages/shared/artifacts/shared-artifact-link";

const viewer = "https://orchestrator.inc/artifact/abcdefghijklmnopqrstuvwxyz";
const link = `ao-app://shared-artifact?url=${encodeURIComponent(viewer)}`;

describe("published artifact links", () => {
	it("opens the exact published viewer, without any session lookup", () => {
		expect(parseSharedArtifactLink(link)).toBe(viewer);
		expect(sharedArtifactAppURL(viewer)).toBe(`${viewer}?inApp=1`);
		expect(sharedArtifactURL("https://orchestrator.inc/artifact/my-renamed-report")).toBe("https://orchestrator.inc/artifact/my-renamed-report");
	});

	it("names the link ID of a published viewer URL, and nothing else", () => {
		expect(sharedArtifactLinkId(viewer)).toBe("abcdefghijklmnopqrstuvwxyz");
		expect(sharedArtifactLinkId("https://orchestrator.inc/artifact/x?vt=1")).toBeNull();
		expect(sharedArtifactLinkId("https://orchestrator.inc/other/abc")).toBeNull();
		expect(sharedArtifactLinkId("http://orchestrator.inc/artifact/abc")).toBeNull();
	});
	it("refuses credentials, tokens, fragments, non-HTTPS and non-artifact destinations", () => {
		for (const value of ["not a URL", viewer.replace("https:", "http:"), viewer.replace("https:", "file:"),
			viewer.replace("https://", "https://user:secret@"), `${viewer}?token=secret`, `${viewer}#fragment`,
			viewer.replace("/artifact/", "/session/"), `${viewer}/extra`, viewer.replace("abcdefghijklmnopqrstuvwxyz", "../private")]) {
			expect(sharedArtifactURL(value)).toBeNull();
			expect(parseSharedArtifactLink(`ao-app://shared-artifact?url=${encodeURIComponent(value)}`)).toBeNull();
		}
	});
	it("refuses ambiguous or unrelated deep links", () => {
		for (const value of [link.replace("ao-app:", "https:"), link.replace("shared-artifact?", "shared-artifact/extra?"),
			`${link}&url=${encodeURIComponent(viewer)}`, `${link}&other=x`, `${link}#fragment`, "ao-app://shared-artifact", "ao-app://callback"]) {
			expect(parseSharedArtifactLink(value)).toBeNull();
		}
	});
	it("opens only a known viewer's pages: production, plus one configured https origin", () => {
		const evil = "https://evil.example/artifact/x";
		expect(parseSharedArtifactLink(`ao-app://shared-artifact?url=${encodeURIComponent(evil)}`)).toBeNull();
		expect(sharedArtifactAppURL(evil)).toBeNull();
		expect(sharedArtifactURL("https://orchestrator.inc.evil.example/artifact/x")).toBeNull();
		const staging = artifactViewerOrigins("https://staging.orchestrator.inc/");
		expect(staging).toEqual(["https://orchestrator.inc", "https://staging.orchestrator.inc"]);
		expect(sharedArtifactAppURL("https://staging.orchestrator.inc/artifact/x", staging)).toBe("https://staging.orchestrator.inc/artifact/x?inApp=1");
		for (const configured of ["http://staging.example", "https://staging.example/path", "https://u:p@staging.example", "not a url", "", undefined]) {
			expect(artifactViewerOrigins(configured)).toEqual(["https://orchestrator.inc"]);
		}
		// Naming a link checks only its shape; opening it checks the origin.
		expect(sharedArtifactLinkId("https://staging.example/artifact/abc")).toBe("abc");
	});
});
