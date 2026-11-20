import { describe, expect, it } from "vitest";
import { cloudAddressIsSafe } from "./config";

describe("cloudAddressIsSafe", () => {
	it("takes https anywhere", () => {
		expect(cloudAddressIsSafe("https://api.aoagents.dev", false)).toBe(true);
	});

	it("takes plain http only to this machine, or in a development build", () => {
		expect(cloudAddressIsSafe("http://localhost:8080", false)).toBe(true);
		expect(cloudAddressIsSafe("http://127.0.0.1:8080", false)).toBe(true);
		// The Android emulator's address for the computer it runs on.
		expect(cloudAddressIsSafe("http://10.0.2.2:8080", false)).toBe(true);
		expect(cloudAddressIsSafe("http://cloud.example.test", false)).toBe(false);
		expect(cloudAddressIsSafe("http://192.168.1.20:8080", true)).toBe(true);
	});

	it("refuses anything that isn't http(s)", () => {
		expect(cloudAddressIsSafe("ftp://cloud.example.test", true)).toBe(false);
		expect(cloudAddressIsSafe("not a url", true)).toBe(false);
	});
});
