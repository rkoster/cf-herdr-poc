import { afterEach, describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const html = readFileSync(
  resolve(process.cwd(), "../docs/presentations/cf-herdr-architecture.html"),
  "utf8",
);

const body = html.match(/<body([^>]*)>([\s\S]*)<script>/);
const script = html.match(/<script>([\s\S]*)<\/script>/);

const openDiagram = () => {
  if (!body || !script) throw new Error("architecture diagram document is incomplete");
  document.body.innerHTML = body[2];
  document.body.dataset.activeStage = "1";
  document.documentElement.className = "no-js";
  Function(script[1])();
};

afterEach(() => {
  document.body.innerHTML = "";
});

describe("architecture presentation", () => {
  it("starts at stage one and progressively reveals later layers", () => {
    openDiagram();
    expect(document.body.dataset.activeStage).toBe("1");
    document.querySelector<HTMLButtonElement>('[data-stage-button="2"]')!.click();
    expect(document.body.dataset.activeStage).toBe("2");
    expect(document.querySelector('[data-stage="1"]')!.getAttribute("aria-hidden")).toBe("false");
    expect(document.querySelector('[data-stage="2"]')!.getAttribute("aria-hidden")).toBe("false");
    expect(document.querySelector('[data-stage="3"]')!.getAttribute("aria-hidden")).toBe("true");
  });

  it("supports number, arrow, space, and reset controls", () => {
    openDiagram();
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "3" }));
    expect(document.body.dataset.activeStage).toBe("3");
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowLeft" }));
    expect(document.body.dataset.activeStage).toBe("2");
    document.dispatchEvent(new KeyboardEvent("keydown", { key: " " }));
    expect(document.body.dataset.activeStage).toBe("3");
    document.querySelector<HTMLButtonElement>("#reset-presentation")!.click();
    expect(document.body.dataset.activeStage).toBe("1");
  });

  it("shows a tooltip on focus and pins detail on activation", () => {
    openDiagram();
    document.querySelector<HTMLButtonElement>('[data-stage-button="2"]')!.click();
    const manager = document.querySelector<SVGGElement>('[data-title="Manager app"]')!;
    manager.dispatchEvent(new FocusEvent("focusin", { bubbles: true }));
    expect(document.querySelector<HTMLElement>("#diagram-tooltip")!.hidden).toBe(false);
    expect(document.querySelector("#diagram-tooltip")!.textContent).toContain("reconciles sandbox lifecycle");
    manager.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    const notes = document.querySelector<HTMLElement>("#speaker-notes")!;
    expect(notes.hidden).toBe(false);
    expect(notes.textContent).toContain("coordinates sandbox lifecycle");
  });
});
