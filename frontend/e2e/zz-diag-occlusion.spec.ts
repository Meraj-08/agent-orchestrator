// TEMPORARY diagnostic for #8029 / PR #8031 review. Not for commit.
// Runs main's single-capture occlusion check on the board archive and records
// what changes between captures. DIAG_PUCKS=keep|unwrap|remove.
import { writeFile } from "node:fs/promises";
import { expect, test, type Locator, type Page } from "@playwright/test";
import { installFakeAgent } from "./support/fake-bridge";

test.use({ userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/130.0.0.0 Safari/537.36" });
const MODE = process.env.DIAG_PUCKS ?? "keep";

async function diffBox(page: Page, a: Buffer, b: Buffer) {
	if (a.equals(b)) return null;
	return page.evaluate(async ([a64, b64]) => {
		const load = async (s: string) => { const i = new Image(); i.src = "data:image/png;base64," + s; await i.decode(); return i; };
		const [ia, ib] = [await load(a64), await load(b64)];
		const w = Math.max(ia.width, ib.width), h = Math.max(ia.height, ib.height);
		const px = (img: HTMLImageElement) => { const c = document.createElement("canvas"); c.width = w; c.height = h; const x = c.getContext("2d")!; x.drawImage(img, 0, 0); return x.getImageData(0, 0, w, h).data; };
		const [da, db] = [px(ia), px(ib)];
		let n = 0, x0 = w, y0 = h, x1 = -1, y1 = -1, maxd = 0;
		for (let y = 0; y < h; y++) for (let x = 0; x < w; x++) {
			const i = (y * w + x) * 4;
			const d = Math.max(Math.abs(da[i] - db[i]), Math.abs(da[i + 1] - db[i + 1]), Math.abs(da[i + 2] - db[i + 2]), Math.abs(da[i + 3] - db[i + 3]));
			if (d) { n++; maxd = Math.max(maxd, d); x0 = Math.min(x0, x); y0 = Math.min(y0, y); x1 = Math.max(x1, x); y1 = Math.max(y1, y); }
		}
		return { size: `${ia.width}x${ia.height} vs ${ib.width}x${ib.height}`, pixels: n, maxDelta: maxd, box: n ? [x0, y0, x1, y1] : null };
	}, [a.toString("base64"), b.toString("base64")]);
}

for (const theme of ["dark", "light"]) {
	for (const transparency of [false, true]) {
		test(`diag archive occlusion: ${theme}, transparency ${transparency}, pucks ${MODE}`, async ({ page }) => {
			await page.setViewportSize({ width: 1400, height: 650 });
			await page.emulateMedia({ reducedMotion: "reduce" });
			await installFakeAgent(page, {
				platform: "MacIntel",
				expandProject: true,
				workers: [
					...Array.from({ length: 12 }, (_, i) => ({ id: `active-${i}`, title: `Active session ${i}`, status: "working" })),
					...Array.from({ length: 8 }, (_, i) => ({ id: `archived-${i}`, title: `Archived session ${i}`, status: "terminated" })),
				],
			});
			await page.addInitScript(({ theme, transparency }) => {
				localStorage.setItem("ao.theme", theme);
				localStorage.setItem("ao.windowTranslucency", String(transparency));
			}, { theme, transparency });
			await page.goto("/#/projects/fake-proj");
			await expect(page.getByTestId("board")).toBeVisible();
			const pucks = await page.evaluate((mode) => {
				const wrap = document.querySelector("[data-swipe-pucks]");
				const found = { wrapper: Boolean(wrap), pucks: document.querySelectorAll("[data-swipe-puck]").length };
				if (wrap && mode === "unwrap") { while (wrap.firstChild) wrap.parentElement!.insertBefore(wrap.firstChild, wrap); wrap.remove(); }
				if (mode === "remove") document.querySelectorAll("[data-swipe-puck]").forEach((p) => p.remove());
				return found;
			}, MODE);
			const toggle = page.getByRole("button", { name: "Archive, 8 sessions", exact: true });
			const archive: Locator = toggle.locator("../..");
			await toggle.click();
			await expect(page.getByRole("list", { name: "Archived sessions" })).toBeVisible();
			// main's check: one capture, hide, one capture
			const before = await archive.screenshot({ animations: "disabled" });
			const cards = page.locator('[data-testid="board-column"] [data-session-id]');
			await cards.evaluateAll((els) => { for (const e of els) (e as HTMLElement).style.visibility = "hidden"; });
			const after = await archive.screenshot({ animations: "disabled" });
			const occl = await diffBox(page, before, after);
			// then a quiet sequence with the cards still hidden: does the archive drift on its own?
			const frames: Buffer[] = [after];
			for (let i = 0; i < 12; i++) { await page.waitForTimeout(50); frames.push(await archive.screenshot({ animations: "disabled" })); }
			const drift: string[] = [];
			for (let i = 1; i < frames.length; i++) { const d = await diffBox(page, frames[i - 1], frames[i]); if (d) drift.push(`f${i}:${JSON.stringify(d)}`); }
			const box = await archive.boundingBox();
			console.log(`DIAG ${theme}/${transparency} pucks=${MODE} ${JSON.stringify(pucks)} archiveBox=${JSON.stringify(box)} occl=${occl ? JSON.stringify(occl) : "same"} drift=${drift.length ? drift.join(" ") : "none"}`);
			if (occl) {
				for (const [n, img] of [["before", before], ["after", after]] as const) await writeFile(test.info().outputPath(`${n}.png`), img);
			}
			expect(occl, "content underneath must not affect the surface's pixels").toBeNull();
		});
	}
}
