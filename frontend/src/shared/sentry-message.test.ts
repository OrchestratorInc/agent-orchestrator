import { describe, expect, it } from "vitest";
import { AoError, sentryErrorCode } from "../../../packages/shared/telemetry/ao-error";
import { sentryMessageIdentity } from "../../../packages/shared/telemetry/sentry-message";

describe("Sentry message normalization", () => {
	it.each([
		[undefined, undefined],
		["", undefined],
		["x".repeat(4097), undefined],
		["Uncaught (in promise) CustomError: Connection failed\nsecond line", "Connection failed"],
		["Uncaught Error: failed\r\nsecond line", "failed"],
		["open http://host/a https://host/b file:///home/a app://renderer/x ws://host wss://host", "open <url> <url> <url> <url> <url> <url>"],
		["send to alice@example.test", "send to <email>"],
		[String.raw`open C:\Users\Alice\file.txt \\host\share\x ~/notes /var/log/app`, "open <path> <path> <path> <path>"],
		["open D:/data/file.txt", "open <path>"],
		["open C:\\Program Files\\Git\\bin.exe failed", "open <path> failed"],
		["open file:/tmp/file", "open <url>"],
		["open /Users/alice/My ConfidentialProject/offer.txt failed", "open <path> failed"],
		["open /Users/alice/My Very ConfidentialProject/offer.txt failed", "open <path> failed"],
		["quote \"prompt\" 'credential' \x60code\x60 ‘value’", "quote '?' '?' '?' '?'"],
		["quote “text”", "quote '?'"],
		["read 12345678-1234-1234-1234-123456789abc and abcDEF1234", "read <id> and <id>"],
		["value 0xdeadbeef was -3.14 at 200", "value <n> was <n> at <n>"],
		["spawn project123-12 next", "spawn <id> next"],
		["spawn comic-12: process exited 5", "spawn <id>: process exited <n>"],
		["  Loading\t failed   now ", "Loading failed now"],
		["Could not open 'private document\u2028also private'", "Could not open '?'"],
		["Couldn't parse 'don't show this' as input", "Couldn't parse '?' as input"],
		["Authorization: Basic YWxpY2U6c2VjcmV0", "<credential>"],
		["Cannot send prompt: summarize this confidential text", "Cannot send <content>"],
		["token=private", "<credential>"],
		["request failed access_token=supersecretvalue", "request failed <credential>"],
		["request failed refresh_token=supersecretvalue", "request failed <credential>"],
		["request failed client_secret=supersecretvalue", "request failed <credential>"],
		["x".repeat(150), "x".repeat(120)],
		["x".repeat(301), undefined],
		["word ".repeat(26), undefined],
	])("normalizes %j", (input, expected) => expect(sentryMessageIdentity(input, undefined, "TypeError").text).toBe(expected));

	it("uses the lowercase normalized text for a fingerprint", () => {
		const fingerprint = (text: string) => sentryMessageIdentity(text).hashInput;
		expect(fingerprint("Spawn comic-12: /home/alice returned 3")).toEqual(fingerprint("spawn comic-31: ~/secret returned 9"));
		expect(fingerprint("Spawn comic-12: /home/alice returned 3")).not.toEqual(fingerprint("Open comic-12: /home/alice returned 3"));
	});

	it("keeps only the normalized hash input for unsafe messages", () => {
		const message = "word ".repeat(26) + " 999";
		expect(sentryMessageIdentity(message)).toMatchObject({ label: "", hashInput: "word ".repeat(26) + "<n>" });
	});

	it("uses the IPC channel and an application code from the cause chain", () => {
		const error = new Error("Error invoking remote method 'remotes:list': Error: translated failure",
			{ cause: new AoError("REMOTES_READ_FAILED", "Original failure") });
		const code = sentryErrorCode(error);
		expect(sentryMessageIdentity(error.message, code)).toMatchObject({ channel: "remotes:list", code: "REMOTES_READ_FAILED", label: "REMOTES_READ_FAILED" });
		expect(sentryMessageIdentity(error.message)).toMatchObject({ channel: "remotes:list", label: "", hashInput: "translated failure" });
		expect(sentryErrorCode({ code: "invalid code!" })).toBeUndefined();
	});
});

it.each([
	"repository acme/secret-project not found",
	"git checkout feature/acme-merger failed",
	"getaddrinfo ENOTFOUND build.acme-internal.corp",
	"Session alice-laptop not found",
	"HTTP 401 Bad credentials sk-ant-abcdefghijklmnop",
])("keeps short uncoded text out of the title", (message) => {
	expect(sentryMessageIdentity(message)).toMatchObject({ label: "", hashInput: expect.any(String) });
});

it.each([
	["SyntaxError", 'Unexpected token \'o\', "ok {"token":"abc"}" is not valid JSON', "unexpected token '?', '?'token'?'abc'?' is not valid json"],
	["SyntaxError", 'Unexpected token \'x\', "x{"custome"... is not valid JSON', "unexpected token '?', '?'custome'?'"],
	["SyntaxError", "Cannot convert acme payroll to a BigInt", "cannot convert acme payroll to a bigint"],
	["TypeError", "Cannot add property acmePayroll, object is not extensible", "cannot add property acmepayroll, object is not extensible"],
])("withholds %s text without changing the hash input", (type, message, hashInput) => {
	expect(sentryMessageIdentity(message, undefined, type)).toMatchObject({ text: undefined, hashInput });
});

it.each([
	["SyntaxError", 'Unexpected token \'P\', "Please sum"... is not valid JSON', "Unexpected token '?', '?'... is not valid JSON"],
	["TypeError", "Cannot read properties of undefined (reading 'id')", "Cannot read properties of undefined (reading '?')"],
])("keeps engine text with unambiguous quotes", (type, message, expected) => {
	expect(sentryMessageIdentity(message, undefined, type).text).toBe(expected);
});

it("counts straight and curly quotes together on the first line only", () => {
	expect(sentryMessageIdentity('Unexpected input “a "b" c”', undefined, "SyntaxError").text).toBeUndefined();
	expect(sentryMessageIdentity('Unexpected input “safe”\n""""', undefined, "SyntaxError").text).toBe("Unexpected input '?'");
});
