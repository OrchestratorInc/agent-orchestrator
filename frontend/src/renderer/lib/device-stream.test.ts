import { describe, expect, it } from "vitest";
import { avcCodecString, findNals, framePayload, MultipartParser } from "./device-stream";

const bytes = (...values: number[]) => new Uint8Array(values);

describe("device stream parsing", () => {
	it("splits an Annex-B access unit into NAL units, 3- and 4-byte start codes alike", () => {
		const unit = bytes(0, 0, 0, 1, 0x67, 0x42, 0xc0, 0x1f, 0, 0, 1, 0x68, 0xce, 0, 0, 0, 1, 0x65, 0x88);
		expect(findNals(unit).map((nal) => nal.type)).toEqual([7, 8, 5]);
	});

	it("names the decoder from the SPS profile, constraints and level", () => {
		const unit = bytes(0, 0, 0, 1, 0x67, 0x42, 0xc0, 0x1f, 0x8c);
		expect(avcCodecString(unit, findNals(unit)[0])).toBe("avc1.42c01f");
	});

	it("strips the hub's v1 SEMU header and leaves raw Annex-B alone", () => {
		const header = new Uint8Array(16);
		new DataView(header.buffer).setUint32(0, 0x53454d55);
		header[4] = 1;
		const payload = bytes(0, 0, 0, 1, 0x65, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10);
		const framed = new Uint8Array([...header, ...payload]);
		expect(Array.from(framePayload(framed))).toEqual(Array.from(payload));
		expect(framePayload(payload)).toBe(payload);
	});

	it("strips the header of a short v1 message by its own length, not a fixed size", () => {
		const header = new Uint8Array(16);
		new DataView(header.buffer).setUint32(0, 0x53454d55);
		header[4] = 1;
		const payload = bytes(0, 0, 0, 1, 0x65, 1, 2, 3);
		expect(Array.from(framePayload(new Uint8Array([...header, ...payload])))).toEqual(Array.from(payload));
		expect(framePayload(header).length).toBe(0);
	});

	it("parses multipart parts by Content-Length, across chunk boundaries", () => {
		const part = (body: string) => `--frame\r\nContent-Type: image/jpeg\r\nContent-Length: ${body.length}\r\n\r\n${body}\r\n`;
		const stream = new TextEncoder().encode(part("AAAA") + part("BBBBBB"));
		const parser = new MultipartParser();
		const first = parser.feed(stream.subarray(0, 30));
		const rest = parser.feed(stream.subarray(30));
		expect([...first, ...rest].map((p) => new TextDecoder().decode(p))).toEqual(["AAAA", "BBBBBB"]);
	});
});
