import { describe, expect, it, beforeEach, afterEach, vi } from "vitest";
import { act, cleanup, render } from "@testing-library/react";
import { MermaidChart } from "./mermaid-chart";

/**
 * Faithful stand-in for mermaid@11 `render()`.
 *
 * The detail that matters: mermaid builds `div#d<id> > svg#<id>`, returns that
 * div's innerHTML, then deletes the div. So the SVG string it hands back
 * CARRIES THE ID WE PASSED IN. Verified against mermaid 11.13.0
 * (`appendDivSvgG` + `removeTempElements` in dist/mermaid.core.mjs).
 *
 * @param id Render id supplied by the component.
 * @returns The svg markup mermaid would return.
 */
function mermaidLikeRender(id: string): { svg: string } {
  const holder = document.createElement("div");
  holder.id = "d" + id;
  holder.innerHTML =
    `<svg id="${id}" width="100%" xmlns="http://www.w3.org/2000/svg">` +
    `<g><rect width="10" height="10"></rect></g></svg>`;
  document.body.appendChild(holder);
  const svg = holder.innerHTML;
  holder.remove();
  return { svg };
}

const mermaidState = {
  parse: true,
  render: mermaidLikeRender as (id: string) => { svg: string },
  ids: [] as string[],
};

vi.mock("mermaid", () => ({
  default: {
    initialize: () => undefined,
    parse: async () => mermaidState.parse,
    render: async (id: string) => {
      mermaidState.ids.push(id);
      return mermaidState.render(id);
    },
  },
}));

/** Drains the component's promise chain, including its `.finally()` pass. */
async function settle() {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
}

const VALID_CHART = "sequenceDiagram\n  participant A\n  A->>A: ping";

beforeEach(() => {
  mermaidState.parse = true;
  mermaidState.render = mermaidLikeRender;
  mermaidState.ids = [];
});

afterEach(() => {
  cleanup();
  document.body.innerHTML = "";
});

describe("MermaidChart", () => {
  it("keeps the rendered diagram instead of blanking the container", async () => {
    const { container } = render(<MermaidChart chart={VALID_CHART} />);
    await settle();

    // Regression: the orphan cleanup used to call getElementById(renderId),
    // which resolves to the SVG mermaid just handed us, and delete it. The
    // user saw a titled, bordered, completely empty box.
    expect(container.querySelector("svg")).not.toBeNull();
    expect(container.textContent).not.toContain("could not be rendered");
  });

  it("still removes the leaked node mermaid strands on document.body", async () => {
    // Mermaid 11 leaves its temp container (and, on the error path, a bomb
    // SVG) attached to body. Narrowing the cleanup must not lose that.
    mermaidState.render = (id: string) => {
      const leak = document.createElement("div");
      leak.id = "d" + id;
      leak.textContent = "Syntax error in text";
      document.body.appendChild(leak);
      return {
        svg: `<svg id="${id}" xmlns="http://www.w3.org/2000/svg"><g></g></svg>`,
      };
    };

    const { container } = render(<MermaidChart chart={VALID_CHART} />);
    await settle();

    const renderId = mermaidState.ids[0];
    expect(renderId).toBeDefined();
    expect(document.getElementById("d" + renderId)).toBeNull();
    expect(document.body.textContent).not.toContain("Syntax error in text");
    expect(container.querySelector("svg")).not.toBeNull();
  });

  it("shows the source when sanitizing leaves nothing to paint", async () => {
    // Anything that survives render but not the sanitizer would otherwise
    // paint an empty box with no explanation.
    mermaidState.render = () => ({ svg: "<script>alert(1)</script>" });

    const { container } = render(<MermaidChart chart={VALID_CHART} />);
    await settle();

    expect(container.textContent).toContain("Diagram could not be rendered");
    expect(container.querySelector("pre")?.textContent).toBe(VALID_CHART);
  });

  it("shows the source when the diagram fails mermaid's parser", async () => {
    mermaidState.parse = false;

    const { container } = render(<MermaidChart chart={"graph TD; A-->"} />);
    await settle();

    expect(container.textContent).toContain("Diagram could not be rendered");
    expect(container.querySelector("pre")?.textContent).toBe("graph TD; A-->");
  });
});
