import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { linkWikiLinks, Markdown, resolveConceptHref } from "../components/Markdown";

describe("resolveConceptHref", () => {
  it.each([
    ["page.md", "area/page"],
    ["../other/x.md", "other/x"],
    ["/x.md", "x"],
    ["sub/index.md", "area/sub"],
    ["page.md#sec", "area/page"],
  ])("%s from area/a is %s", (href, want) => {
    expect(resolveConceptHref(href, "area/a")).toBe(want);
  });
  it.each(["https://example.com/x.md", "//host/x.md", "#frag", "file.pdf", "mailto:a@example.com", "page"])(
    "leaves %s alone",
    (href) => expect(resolveConceptHref(href, "area/a")).toBeNull(),
  );
  it("decodes the path, and keeps a malformed escape as written", () => {
    expect(resolveConceptHref("my%20page.md", "area/a")).toBe("area/my page");
    expect(resolveConceptHref("bad%E0%A4%A.md", "area/a")).toBe("area/bad%E0%A4%A");
  });

  it("an expanded concept resolves against the base the server resolved", () => {
    expect(resolveConceptHref("s.md", "map/c", ["map/c/s"])).toBe("map/c/s");
    expect(resolveConceptHref("s.md", "map/c", ["map/s"])).toBe("map/s");
  });
});

describe("links in a concept body", () => {
  it("a relative .md link navigates inside the Atlas and a wiki-link carries the kb", async () => {
    const onNavigate = vi.fn();
    render(
      <Markdown
        conceptId="area/a"
        onNavigate={onNavigate}
        hrefOf={(id) => `?kb=kb-a&concept=${encodeURIComponent(id)}`}
      >
        {"[p](page.md) and [[infra/x]] and [e](https://example.com)"}
      </Markdown>,
    );
    await userEvent.click(screen.getByText("p"));
    expect(onNavigate).toHaveBeenCalledWith("area/page");
    expect(screen.getByText("infra/x").getAttribute("href")).toBe("?kb=kb-a&concept=infra%2Fx");
    expect(screen.getByText(/^e/).getAttribute("href")).toBe("https://example.com");
  });

  it("leaves a wiki-link literal inside a ~~~ fence and a double-backtick span", () => {
    expect(linkWikiLinks("~~~\n[[a/b]]\n~~~\nx")).toBe("~~~\n[[a/b]]\n~~~\nx");
    expect(linkWikiLinks("see ``[[a/b]] ` x`` and [[c/d]]")).toBe(
      "see ``[[a/b]] ` x`` and [c/d](concept-link/c%2Fd)",
    );
  });

  it("a fence closes on the opener's character and length, not on any run", () => {
    expect(linkWikiLinks("````\n```\n[[a]]\n```\n[[b]]\n````\n[[c]]")).toBe(
      "````\n```\n[[a]]\n```\n[[b]]\n````\n[c](concept-link/c)",
    );
    expect(linkWikiLinks("~~~\n```\n[[a]]\n~~~\n[[b]]")).toBe("~~~\n```\n[[a]]\n~~~\n[b](concept-link/b)");
  });

  it("an inline span does not cross a paragraph break", () => {
    expect(linkWikiLinks("a `x\n\n[[a/b]] and `y`")).toBe("a `x\n\n[a/b](concept-link/a%2Fb) and `y`");
  });

  it("no node attribute reaches the DOM", () => {
    const { container } = render(<Markdown conceptId="a/b">{"[x](y.md) [e](https://example.com)"}</Markdown>);
    expect(container.querySelector("[node]")).toBeNull();
  });
});

describe("a malformed wiki-link target", () => {
  it.each(["%FF", "%C3", "a%C3", "%E0%A4%A"])("%s renders as plain text without throwing", (esc) => {
    const { container } = render(<Markdown conceptId="a/b">{`see [label](concept-link/${esc}) end`}</Markdown>);
    expect(container.textContent).toContain("label");
    expect(container.querySelector("a")).toBeNull();
  });
  it("a valid escape still navigates", () => {
    const { container } = render(<Markdown onNavigate={() => {}}>{"[[a b/c]]"}</Markdown>);
    expect(container.querySelector("a.markdown__wikilink")).not.toBeNull();
  });
});

describe("linkWikiLinks cross", () => {
  // AC-cross-kb-links-21
  const edge = (state: "resolved" | "missing" | "unresolved" | "out_of_scope", kb = "kb-b", id = "ops/x") => [
    { direction: "out" as const, kb, id, state },
  ];
  it("renders a resolved cross link as a link", () => {
    expect(linkWikiLinks("see [[kb:kb-b/ops/x]]", edge("resolved"))).toBe(
      "see [kb:kb-b/ops/x](cross-link/kb-b/ops%2Fx)",
    );
    expect(linkWikiLinks("see [[kb:kb-b/ops/x|the runbook]]", edge("resolved"))).toBe(
      "see [the runbook](cross-link/kb-b/ops%2Fx)",
    );
  });
  it.each(["unresolved", "missing", "out_of_scope"] as const)("renders a %s link as plain text", (state) => {
    expect(linkWikiLinks("see [[kb:kb-b/ops/x]]", edge(state))).toBe("see kb:kb-b/ops/x");
  });
  it("leaves a code span, an unlisted link and a server-qualified one as text without an href", () => {
    expect(linkWikiLinks("`[[kb:kb-b/ops/x]]`", edge("resolved"))).toBe("`[[kb:kb-b/ops/x]]`");
    expect(linkWikiLinks("[[kb:kb-b/ops/y]]", edge("resolved"))).toBe("kb:kb-b/ops/y");
    expect(linkWikiLinks("[[kb:kb-b/ops/x]]")).toBe("kb:kb-b/ops/x");
    expect(linkWikiLinks("[[kb:kb-b@host/ops/x]]", edge("resolved"))).toBe("kb:kb-b@host/ops/x");
  });
  it("follows a resolved link into the other KB, and draws the others with no href", async () => {
    const onOpenCross = vi.fn();
    render(
      <Markdown
        cross={[...edge("resolved"), { direction: "out", kb: "kb-c", id: "a", state: "unresolved" }]}
        onOpenCross={onOpenCross}
      >
        {"[[kb:kb-b/ops/x]] and [[kb:kb-c/a]]"}
      </Markdown>,
    );
    await userEvent.click(screen.getByRole("link", { name: "kb:kb-b/ops/x" }));
    expect(onOpenCross).toHaveBeenCalledWith("kb-b", "ops/x");
    expect(screen.getAllByRole("link")).toHaveLength(1);
    expect(screen.getByText(/kb:kb-c\/a/)).toBeTruthy();
  });
});
