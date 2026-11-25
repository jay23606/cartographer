import type { ExternalEdge, GraphSnapshot } from "../api/types";

/** The id of the stub node a resolved cross-KB edge ends at. It starts with
 *  `kb:`, which no concept id of this KB can (an id is a path), so a click on
 *  it is told apart from a concept without a lookup. */
export function stubId(kb: string, id: string): string {
  return `kb:${kb}/${id}`;
}

/** Splits a stub id back into the KB and the page, or null for a concept id. */
export function parseStubId(id: string): { kb: string; id: string } | null {
  if (!id.startsWith("kb:")) return null;
  const slash = id.indexOf("/");
  return slash > 3 ? { kb: id.slice(3, slash), id: id.slice(slash + 1) } : null;
}

/**
 * The snapshot with the resolved external edges drawn as small stub nodes
 * labelled with the page id. Only `resolved` edges of a page already in the
 * snapshot are drawn: every other state is nothing here, never a title, never
 * a count (the KB-level map belongs to the KB map).
 */
export function withExternalEdges(snapshot: GraphSnapshot, external: readonly ExternalEdge[] | null): GraphSnapshot {
  if (!external || external.length === 0) return snapshot;
  const known = new Set(snapshot.nodes.map((n) => n.id));
  const nodes = [...snapshot.nodes];
  const edges = [...snapshot.edges];
  const stubs = new Set<string>();
  for (const e of external) {
    if (e.state !== "resolved" || !known.has(e.from)) continue;
    const stub = stubId(e.kb, e.id);
    if (!stubs.has(stub)) {
      stubs.add(stub);
      nodes.push({ id: stub, title: e.id, in_degree: 0, out_degree: 0 });
    }
    edges.push(e.direction === "in" ? { source: stub, target: e.from } : { source: e.from, target: stub });
  }
  return stubs.size === 0 ? snapshot : { ...snapshot, nodes, edges };
}
