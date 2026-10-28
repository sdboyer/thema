import { render } from "preact";
import { act } from "preact/test-utils";
import { afterEach, describe, expect, it, vi } from "vitest";
import { steps } from "./steps";
import { Tour } from "./Tour";

function mount(onClose = () => {}) {
  const root = document.createElement("div");
  document.body.append(root);
  render(<Tour onClose={onClose} />, root);
  return root;
}

afterEach(() => vi.unstubAllGlobals());

describe("Tour", () => {
  it("lists every step, in order", () => {
    const root = mount();
    expect([...root.querySelectorAll(".step h3")].map((h) => h.firstChild!.textContent)).toEqual(steps.map((s) => s.title));
  });

  it("shows backticked text as code", () => {
    const root = mount();
    const first = root.querySelector(".step .todo")!;
    expect(first.querySelector("code")!.textContent).toBe("ship");
    expect(first.textContent).not.toContain("`");
  });

  it("copies a snippet's text exactly", async () => {
    const writeText = vi.fn(async () => {});
    vi.stubGlobal("navigator", { clipboard: { writeText } });
    const root = mount();
    const step = steps.find((s) => s.snippets)!;
    const button = root.querySelector<HTMLButtonElement>(`#tour-${step.id} .snippet button`)!;
    await act(async () => button.click());
    expect(writeText).toHaveBeenCalledWith(step.snippets![0]!.text);
    expect(button.textContent).toBe("Copied");
  });

  it("closes", () => {
    const onClose = vi.fn();
    const root = mount(onClose);
    root.querySelector<HTMLButtonElement>(".tour .close")!.click();
    expect(onClose).toHaveBeenCalled();
  });
});
