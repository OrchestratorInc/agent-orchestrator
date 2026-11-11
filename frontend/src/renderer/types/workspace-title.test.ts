import { afterEach, expect, it } from "vitest";
import { appI18n } from "../i18n/instance";
import { sessionDisplayTitle } from "./workspace";

afterEach(async () => { await appI18n.changeLanguage("en"); });

it("保留独立编排器的任务名及手动改名，不附加完整 ID", () => {
	const session = { id: "project-independent-orchestrator-42", independentOrchestrator: true, title: "修复登录" };
	expect(sessionDisplayTitle(session)).toBe("修复登录");
	expect(sessionDisplayTitle({ ...session, title: "人工整理名称" })).toBe("人工整理名称");
	expect(sessionDisplayTitle({ ...session, independentOrchestrator: false, title: session.id })).toBe(session.id);
});

it("独立编排器的空名和 ID 回退均显示随语言切换的简短默认名", async () => {
	const session = { id: "project-independent-orchestrator-42", independentOrchestrator: true, title: "  " };
	expect(sessionDisplayTitle(session)).toBe("Untitled task");
	await appI18n.changeLanguage("zh-CN");
	expect(sessionDisplayTitle({ ...session, title: session.id })).toBe("未命名任务");
});

it("主编排器和旧版编排器的 ID 回退均可读，已有名称仍保留", async () => {
	const session = { id: "project-1", title: "project-1", kind: "orchestrator" as const };
	expect(sessionDisplayTitle(session)).toBe("Orchestrator");
	expect(sessionDisplayTitle({ ...session, title: "项目协调" })).toBe("项目协调");
	await appI18n.changeLanguage("zh-CN");
	expect(sessionDisplayTitle({ id: "project-orchestrator", title: " " })).toBe("编排器");
});
