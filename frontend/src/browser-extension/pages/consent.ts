import { chromeApi } from "../chrome-api";

const chrome = chromeApi();
const sessionId = new URLSearchParams(location.search).get("session") ?? "";
document.getElementById("session")!.textContent = sessionId;
const error = document.getElementById("error")!;
const allow = document.getElementById("allow") as HTMLButtonElement;
const deny = document.getElementById("deny") as HTMLButtonElement;
const trusted = document.getElementById("trusted") as HTMLInputElement;

async function decide(allowed: boolean): Promise<void> {
	allow.disabled = deny.disabled = true;
	try {
		await chrome.runtime.sendMessage({ type: "ao-consent-decision", sessionId, allow: allowed, trusted: allowed && trusted.checked });
		window.close();
	} catch (cause) {
		error.textContent = String(cause);
		allow.disabled = deny.disabled = false;
	}
}

allow.addEventListener("click", () => void decide(true));
deny.addEventListener("click", () => void decide(false));
allow.focus();
