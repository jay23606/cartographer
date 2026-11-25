import { describe, expect, it } from "vitest";
import type { ExternalEdge, GraphSnapshot } from "../api/types";
import { parseStubId, withExternalEdges } from "../lib/external";

const snap: GraphSnapshot = {
  nodes: [{ id: "ops/a", in_degree: 0, out_degree: 0 }],
  edges: [],
  total_nodes: 1,
  total_edges: 0,
  limit: 10,
};
const e = (state: ExternalEdge["state"], from = "ops/a"): ExternalEdge => ({ from, direction: "out", kb: "kb-b", id: "ops/x", state });

describe("withExternalEdges", () => {
  it("draws a resolved edge as a stub labelled with the id, and nothing for any other state", () => {
    const drawn = withExternalEdges(snap, [e("resolved")]);
    expect(drawn.nodes.map((n) => n.id)).toEqual(["ops/a", "kb:kb-b/ops/x"]);
    expect(drawn.nodes[1]?.title).toBe("ops/x");
    expect(drawn.edges).toEqual([{ source: "ops/a", target: "kb:kb-b/ops/x" }]);
    for (const state of ["missing", "unresolved", "out_of_scope"] as const) {
      expect(withExternalEdges(snap, [e(state)])).toBe(snap);
    }
    expect(withExternalEdges(snap, [e("resolved", "ops/not-drawn")])).toBe(snap);
  });
  it("tells a stub from a concept id", () => {
    expect(parseStubId("kb:kb-b/ops/x")).toEqual({ kb: "kb-b", id: "ops/x" });
    expect(parseStubId("ops/a")).toBeNull();
  });
});
