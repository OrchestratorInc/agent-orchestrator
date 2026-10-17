import { describe, expect, it } from "vitest";
import { createPrivateKey, createPublicKey, sign, verify, X509Certificate } from "node:crypto";
import { previewCertificate, trustPreviewCertificate } from "./preview-certificate";

type VerifyProc = (request: { hostname: string; certificate: { data: string } }, callback: (result: number) => void) => void;

function fakeSession() {
	const procs: VerifyProc[] = [];
	return {
		procs,
		session: { setCertificateVerifyProc: (proc: VerifyProc) => procs.push(proc) },
		verdict(hostname: string, data: string): number {
			let result = Number.NaN;
			procs.at(-1)?.({ hostname, certificate: { data } }, (value) => (result = value));
			return result;
		},
	};
}

describe("previewCertificate", () => {
	it("is a valid self-signed loopback certificate for its own key", () => {
		const { cert, key } = previewCertificate();
		const parsed = new X509Certificate(cert);
		expect(parsed.verify(parsed.publicKey)).toBe(true);
		expect(parsed.subjectAltName).toContain("DNS:*.localhost");
		expect(new Date(parsed.validFrom).getTime()).toBeLessThan(Date.now());
		expect(new Date(parsed.validTo).getTime()).toBeGreaterThan(Date.now());
		const signature = sign("sha256", Buffer.from("probe"), createPrivateKey(key));
		expect(verify("sha256", Buffer.from("probe"), createPublicKey(cert), signature)).toBe(true);
		expect(previewCertificate()).toBe(previewCertificate());
	});
});

describe("trustPreviewCertificate", () => {
	const preview = "ao-preview-0123456789abcdef0123456789abcdef.localhost";

	it("trusts only the preview certificate, and only on preview hosts", () => {
		const { cert } = previewCertificate();
		const lines = cert.split("\n");
		lines[2] = (lines[2][0] === "A" ? "B" : "A") + lines[2].slice(1); // one base64 character differs
		const other = lines.join("\n");
		const { session, verdict } = fakeSession();
		trustPreviewCertificate(session);
		expect(verdict(preview, cert)).toBe(0);
		expect(verdict(preview, cert.replace(/\n/g, "\r\n"))).toBe(0);
		expect(verdict(preview, other)).toBe(-3);
		expect(verdict("example.com", cert)).toBe(-3);
		expect(verdict("ao-preview-artifact.mfxs2mi.localhost", cert)).toBe(-3);
		expect(verdict(`evil.${preview}`, cert)).toBe(-3);
	});

	it("installs its handler once per session", () => {
		const { session, procs } = fakeSession();
		trustPreviewCertificate(session);
		trustPreviewCertificate(session);
		expect(procs).toHaveLength(1);
	});
});
