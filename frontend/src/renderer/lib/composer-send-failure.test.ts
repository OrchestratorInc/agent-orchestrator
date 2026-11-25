import { describe, expect, it } from "vitest";
import { COMPOSER_ERROR_CODES, SEND_REJECTION_CODES, sendRejectionCode } from "./composer-send-failure";
import { isErrorCode } from "./ui-usage-registry";

describe("composer send failure codes", () => {
	it("reports a known refusal by its own code, in upper case", () => {
		expect(sendRejectionCode("CHAT_OPEN_ELSEWHERE")).toBe("CHAT_OPEN_ELSEWHERE");
		expect(sendRejectionCode("validation_error")).toBe("VALIDATION_ERROR");
	});

	it("reports an unknown or missing refusal code as SEND_REJECTED", () => {
		expect(sendRejectionCode("SOMETHING_NEW")).toBe("SEND_REJECTED");
		expect(sendRejectionCode(undefined)).toBe("SEND_REJECTED");
	});

	it("lets every composer code through the error_code allowlist", () => {
		for (const code of COMPOSER_ERROR_CODES) expect(isErrorCode(code)).toBe(true);
		for (const code of SEND_REJECTION_CODES) expect(isErrorCode(sendRejectionCode(code))).toBe(true);
	});
});
