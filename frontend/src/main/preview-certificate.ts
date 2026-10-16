import { generateKeyPairSync, randomBytes, sign, X509Certificate } from "node:crypto";
import type { Session } from "electron";

// Remote previews reach the browser panel over HTTPS on loopback so Chromium
// speaks HTTP/2 to them. Over HTTP/1.1 it opens at most six connections per
// host, and a dev server's hundreds of modules then cost a round trip to the
// remote host for every six requests.
//
// The certificate is self-signed, made once per process, and trusted only for
// preview hosts and only when it is exactly this certificate. Node can sign
// but has no certificate builder, so the DER is assembled here.

export type PreviewCertificate = { cert: string; key: string };

const PREVIEW_HOST = /^ao-preview-[0-9a-f]{32}\.localhost$/;

let certificate: PreviewCertificate | undefined;

export function previewCertificate(): PreviewCertificate {
	certificate ??= createPreviewCertificate();
	return certificate;
}

type CertificateVerifier = Pick<Session, "setCertificateVerifyProc">;
const trusted = new WeakSet<CertificateVerifier>();

// Trusts the preview certificate in one browser session. Every other host and
// certificate keeps Chromium's own verification (-3).
export function trustPreviewCertificate(session: CertificateVerifier): void {
	if (trusted.has(session)) return;
	trusted.add(session);
	session.setCertificateVerifyProc((request, callback) => {
		const preview = PREVIEW_HOST.test(request.hostname) && certificate !== undefined &&
			pemBody(request.certificate.data) === pemBody(certificate.cert);
		callback(preview ? 0 : -3);
	});
}

function pemBody(pem: string): string {
	return pem.replace(/-----[A-Z ]+-----|\s/g, "");
}

function createPreviewCertificate(): PreviewCertificate {
	const { privateKey, publicKey } = generateKeyPairSync("ec", { namedCurve: "P-256" });
	const ecdsaWithSha256 = sequence(objectId("1.2.840.10045.4.3.2"));
	const name = sequence(set(sequence(objectId("2.5.4.3"), tlv(0x0c, Buffer.from("AO preview")))));
	const now = Date.now();
	const subjectAltName = sequence(
		objectId("2.5.29.17"),
		tlv(0x04, sequence(tlv(0x82, Buffer.from("localhost")), tlv(0x82, Buffer.from("*.localhost")))),
	);
	const serial = randomBytes(16);
	serial[0] = (serial[0] & 0x7f) | 0x40; // positive INTEGER in minimal DER: the first byte is never 00
	const tbs = sequence(
		tlv(0xa0, tlv(0x02, Buffer.from([2]))), // v3
		tlv(0x02, serial),
		ecdsaWithSha256,
		name,
		sequence(utcTime(new Date(now - 60 * 60_000)), utcTime(new Date(now + 365 * 24 * 60 * 60_000))),
		name,
		publicKey.export({ type: "spki", format: "der" }),
		tlv(0xa3, sequence(subjectAltName)),
	);
	const der = sequence(tbs, ecdsaWithSha256, tlv(0x03, Buffer.concat([Buffer.from([0]), sign("sha256", tbs, privateKey)])));
	const cert = `-----BEGIN CERTIFICATE-----\n${der.toString("base64").match(/.{1,64}/g)?.join("\n")}\n-----END CERTIFICATE-----\n`;
	if (!new X509Certificate(cert).verify(publicKey)) throw new Error("preview certificate failed its own signature check");
	return { cert, key: String(privateKey.export({ type: "pkcs8", format: "pem" })) };
}

function tlv(tag: number, ...parts: Buffer[]): Buffer {
	const body = Buffer.concat(parts);
	const n = body.length;
	const length = n < 0x80 ? [n] : n < 0x100 ? [0x81, n] : [0x82, n >> 8, n & 0xff];
	return Buffer.concat([Buffer.from([tag, ...length]), body]);
}

function sequence(...parts: Buffer[]): Buffer {
	return tlv(0x30, ...parts);
}

function set(...parts: Buffer[]): Buffer {
	return tlv(0x31, ...parts);
}

function objectId(dotted: string): Buffer {
	const [first, second, ...rest] = dotted.split(".").map(Number);
	const bytes = [40 * first + second];
	for (const value of rest) {
		const chunk = [value & 0x7f];
		for (let v = value >> 7; v > 0; v >>= 7) chunk.unshift((v & 0x7f) | 0x80);
		bytes.push(...chunk);
	}
	return tlv(0x06, Buffer.from(bytes));
}

function utcTime(date: Date): Buffer {
	return tlv(0x17, Buffer.from(`${date.toISOString().replace(/[-:T]/g, "").slice(2, 14)}Z`));
}
