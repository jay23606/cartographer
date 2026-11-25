import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import type { Concept } from "../api/types";
import { Inspector } from "../components/Inspector";
import { json, stubApi } from "./fixtures";

// The graph is a list of its node ids with the two gestures a node takes.
vi.mock("../components/GraphView", () => ({
  GraphView: (p: { snapshot: { nodes: { id: string }[] }; onSelect(id: string): void; onExpand?(id: string): void }) => (
    <ul aria-label="drawn nodes">
      {p.snapshot.nodes.map((n) => (
        <li key={n.id}>
          <span>{n.id}</span>
          <button onClick={() => p.onExpand?.(n.id)}>expand {n.id}</button>
        </li>
      ))}
    </ul>
  ),
}));

const stub = { edges: [{ from: "infra/a", direction: "out", kb: "kb-x", id: "ops/x", state: "resolved" }], limit: 2000, truncated: false };
// The default routes, with two KBs listed and the given overrides first.
function api(over: (url: string) => Response | undefined) {
  const base = stubApi();
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) => {
      if (url.endsWith("/kbs")) return json({ kbs: [{ name: "kb-a", status: "normal", ready: true }, { name: "kb-b", status: "normal", ready: true }] });
      return over(url) ?? base(url);
    }),
  );
}

describe("the external stubs of the graph", () => {
  beforeEach(() => {
    window.history.replaceState(null, "", "/ui/?kb=kb-a");
    localStorage.clear();
    sessionStorage.clear();
  });
  afterEach(() => vi.unstubAllGlobals());

  const switchToB = () => {
    window.history.pushState(null, "", "/ui/?kb=kb-b");
    act(() => {
      window.dispatchEvent(new PopStateEvent("popstate"));
    });
  };

  it("drops the stubs of the old KB on a switch, and draws none when the new one fails", async () => {
    api((url) => {
      if (url.includes("/kbs/kb-a/graph/external")) return json(stub);
      if (url.includes("/kbs/kb-b/graph/external")) return json({ error: { code: "error", message: "boom" } }, 500);
    });
    render(<App />);
    expect(await screen.findByText("kb:kb-x/ops/x")).toBeInTheDocument();
    switchToB();
    await waitFor(() => expect(screen.queryByText("kb:kb-x/ops/x")).toBeNull());
    await new Promise((r) => setTimeout(r, 50));
    expect(screen.queryByText("kb:kb-x/ops/x")).toBeNull();
    expect(screen.getByText("infra/a")).toBeInTheDocument();
  });

  it("a double click on a stub opens nothing in the current KB", async () => {
    api((url) => (url.includes("/kbs/kb-a/graph/external") ? json(stub) : undefined));
    render(<App />);
    await userEvent.click(await screen.findByRole("button", { name: "expand kb:kb-x/ops/x" }));
    expect(window.location.search).not.toContain("concept=");
    await userEvent.click(screen.getByRole("button", { name: "expand infra/a" }));
    await waitFor(() => expect(window.location.search).toContain("concept=infra%2Fa"));
  });
});

describe("the Inspector cross-KB rows", () => {
  const concept = {
    id: "infra/a",
    title: "Alpha",
    collection: "infra",
    frontmatter: {},
    body: "x",
    body_bytes: 1,
    outline: [],
    content_hash: "h",
    outbound: [],
    inbound: [],
    broken: [],
    cross: [
      { direction: "out", kb: "kb-b", id: "no-state" },
      { direction: "out", kb: "kb-b", id: "ok", state: "resolved" },
      { direction: "in", kb: "kb-b", id: "from-b" },
    ],
  } as Concept;
  afterEach(() => vi.unstubAllGlobals());

  it("links an outbound row only when it is resolved, an inbound one always", () => {
    vi.stubGlobal("fetch", stubApi());
    render(
      <Inspector kb="kb-a" conceptId="infra/a" concept={concept} error={null} loading={false} findings={[]} onNavigate={() => {}} onPreview={() => {}} onClose={() => {}} onOpenCross={() => {}} />,
    );
    expect(screen.getByText("no-state", { exact: false, selector: ".inspector__link-id" }).closest("button")).toBeNull();
    expect(screen.getByText("ok", { exact: false, selector: ".inspector__link-id" }).closest("button")!).toBeEnabled();
    expect(screen.getByText("from-b", { exact: false, selector: ".inspector__link-id" }).closest("button")!).toBeEnabled();
  });
});
