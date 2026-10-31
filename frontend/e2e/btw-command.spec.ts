import { _electron as electron, expect, test as base } from "@playwright/test";
import path from "node:path";
import { installFakeAgent, installFakeBridge } from "./support/fake-bridge";
import { agentReadiness } from "../src/renderer/test/agent-readiness-fixtures";

const test = process.env.AO_E2E_ELECTRON ? base.extend({
	page: async ({}, use) => {
		const app = await electron.launch({
			executablePath: process.env.AO_E2E_ELECTRON_PATH ?? require("electron") as string,
			args: [path.join(__dirname, "support/electron-shell.cjs")],
		});
		try { const page = await app.firstWindow(); await page.bringToFront(); await use(page); }
		finally { await app.close(); }
	},
}) : base;

for (const completion of ["Enter", "Tab"]) test(`@P0 /side discovers /btw and ${completion} completes before creating`, async ({ page }) => {
	page.setDefaultNavigationTimeout(90_000);
	page.on("pageerror", error => { throw error; });
	await installFakeBridge(page);
	await installFakeAgent(page, {projectId:"btw",projectName:"btw",workers:[{id:"btw-worker",provider:"codex",title:"Command test",mode:"chat"}]});
	await page.addInitScript(() => {
		localStorage.setItem("ao.developerMode","true"); localStorage.setItem("ao.sideChats","true");
	});
	const sides = [{id:"existing",sessionId:"btw-worker",mainConversationId:"main",state:"ready",label:"Existing side"}];
	const created: unknown[] = [], sent: {text?:string}[] = [];
	await page.route("http://127.0.0.1:8080/api/v1/**", async route => {
		const request=route.request(), url=new URL(request.url()), pathname=url.pathname;
		if(pathname.endsWith("/agents/installers")) return route.fulfill({json:{agents:[]}});
		if(pathname.endsWith("/agents/auth-plans")) return route.fulfill({json:{plans:[]}});
		if(pathname.endsWith("/agents/install-jobs")) return route.fulfill({json:{jobs:[]}});
		if(request.headers().accept?.includes("text/event-stream")) return route.fulfill({contentType:"text/event-stream",body:"retry: 600000\n: connected\n\n"});
		if(pathname.endsWith("/agents/readiness") || pathname.endsWith("/agents/readiness/ensure")) return route.fulfill({json:{agents:[agentReadiness("codex","Codex")]}});
		if(pathname==="/api/v1/projects/btw") return route.fulfill({json:{status:"ok",project:{id:"btw",agent:"codex",config:{worker:{agent:"codex"}}}}});
		if(pathname.endsWith("/conversation")) return route.fulfill({json:{conversationId:"main",sessionId:"btw-worker",harness:"codex",mode:"chat",controller:"ready",capabilities:["resume"],latestSequence:0,oldestSequence:0,hasMoreBefore:false,turns:[],messages:[],activities:[],settings:{}}});
		if(pathname.endsWith("/models")) return route.fulfill({json:{models:[],selected:{}}});
		if(pathname.endsWith("/skills")) return route.fulfill({json:{skills:[]}});
		if(pathname.endsWith("/side-chats")) {
			if(request.method()==="POST") {created.push(request.postDataJSON()); const side={...sides[0],id:`new-${created.length}`,label:`New side ${created.length}`}; sides.push(side); return route.fulfill({json:{side}});}
			return route.fulfill({json:{sides}});
		}
		if(pathname.endsWith("/messages") && request.method()==="POST") {sent.push(request.postDataJSON());return route.fulfill({json:{status:"ok"}});}
		if(pathname.endsWith("/draft")) return route.fulfill({json:request.method()==="GET"?{contentJson:""}:{}});
		if(pathname.includes("/side-chats/")) return route.fulfill({json:{snapshot:{side:sides.find(side=>pathname.endsWith(`/${side.id}`))??sides[0],turns:[],messages:[],activities:[]}}});
		return route.fulfill({json:{status:"ok"}});
	});
	await page.goto(`http://127.0.0.1:${process.env.AO_E2E_PORT??5173}/#/projects/btw/sessions/btw-worker`,{waitUntil:"domcontentloaded"});
	const main=page.getByRole("combobox",{name:"Message the agent"});
	await expect(main).toBeVisible({timeout:15_000}); await main.fill("/side");
	await expect(page.getByRole("option",{name:/\/btw/})).toBeVisible();
	await expect(page.getByRole("option",{name:/^\/side\b/})).toHaveCount(0);
	await page.screenshot({path:test.info().outputPath("btw-discovery.png")});
	await main.press(completion);
	await expect(main).toContainText("/btw"); expect(created).toHaveLength(0); expect(sent).toHaveLength(0);
	await page.keyboard.type("explain this"); await main.press("Enter");
	await expect.poll(()=>created.length).toBe(1); await expect.poll(()=>sent.length).toBe(1);
	expect(sent[0].text).toBe("explain this");
	const side=page.getByRole("combobox",{name:"Side chat question"});
	await expect(side).toBeVisible(); await side.fill("/side");
	await expect(page.getByRole("option",{name:/\/btw/})).toBeVisible();
	await side.press(completion); await expect(side).toContainText("/btw");
	expect(created).toHaveLength(1); expect(sent).toHaveLength(1);
	await page.screenshot({path:test.info().outputPath("btw-side-completion.png")});
	await page.keyboard.type("follow up"); await side.press("Enter");
	await expect.poll(()=>created.length).toBe(2); await expect.poll(()=>sent.length).toBe(2);
	expect(sent[1].text).toBe("follow up");
});
