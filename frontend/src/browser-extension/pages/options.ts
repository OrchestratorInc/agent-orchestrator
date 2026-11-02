import { chromeApi } from "../chrome-api";
import { parseHostList, type SitePolicy } from "../policy";

const chrome = chromeApi();
const field = (id: string) => document.getElementById(id) as HTMLTextAreaElement;
const trusted = document.getElementById("trusted") as HTMLInputElement;
const saved = document.getElementById("saved")!;
let defaults: SitePolicy | undefined;

function show(policy: SitePolicy): void {
	field("deny").value = policy.deny.join("\n");
	field("allow").value = policy.allow.join("\n");
	field("synthetic").value = policy.syntheticInput.join("\n");
	trusted.checked = policy.trustedInput;
}

async function load(): Promise<void> {
	const response = (await chrome.runtime.sendMessage({ type: "ao-ui-get-policy" })) as { policy: SitePolicy; defaults: SitePolicy };
	defaults = response.defaults;
	show(response.policy);
}

document.getElementById("save")!.addEventListener("click", async () => {
	const policy: SitePolicy = {
		deny: parseHostList(field("deny").value),
		allow: parseHostList(field("allow").value),
		syntheticInput: parseHostList(field("synthetic").value),
		trustedInput: trusted.checked,
	};
	await chrome.runtime.sendMessage({ type: "ao-ui-set-policy", policy });
	saved.textContent = "Saved.";
	setTimeout(() => (saved.textContent = ""), 2_000);
});
document.getElementById("reset")!.addEventListener("click", () => {
	if (defaults) show(defaults);
	saved.textContent = "Defaults restored; save to keep them.";
});
void load();
