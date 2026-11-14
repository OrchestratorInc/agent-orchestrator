import { describe, expect, it } from "vitest";
import { Archive, ChartBar, Code2, Database, File, FileCode2, FileText, Image, Music, Presentation, Table2, Video } from "@/components/app-icons";
import { artifactFileIcon } from "./artifact-file-icon";

describe("artifactFileIcon", () => {
	it.each([
		["html", "dashboard.html", FileCode2],
		["file", "legacy.HTM", FileCode2],
		["markdown", "notes.md", FileText],
		["file", "guide.markdown", FileText],
		["file", "report.pdf", File],
		["file", "hero.png", Image],
		["file", "logo.svg", Image],
		["file", "demo.MP4", Video],
		["file", "clip.webm", Video],
		["file", "voice.mp3", Music],
		["file", "flow.mmd", ChartBar],
		["file", "metrics.csv", Table2],
		["file", "budget.xlsx", Table2],
		["file", "pitch.pptx", Presentation],
		["file", "bundle.tar.gz", Archive],
		["file", "config.yaml", Database],
		["file", "data.json", Database],
		["file", "script.py", Code2],
		["file", "view.tsx", Code2],
		["file", "unknown.bin", File],
		["file", "README", File],
	] as const)("gives a %s artifact at %s its type's icon", (kind, path, icon) => {
		expect(artifactFileIcon({ kind, path })).toBe(icon);
	});
});
