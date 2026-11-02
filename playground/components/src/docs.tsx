// Where the page points for what thema's terms mean: thema's own docs, which
// live in this tree beside the playground, and pkg.go.dev for what only the
// Go API documents.

// docsBase is where thema's docs/ is published.
export const docsBase = "https://github.com/sdboyer/thema/blob/main/docs/";

// themaDoc is the URL of a file under thema's docs/.
export function themaDoc(path: string): string {
  return docsBase + path;
}

// ref is the URL of an entry in thema's reference docs, docs/reference/.
function ref(page: string, anchor = ""): string {
  return themaDoc(`reference/${page}${anchor ? "#" + anchor : ""}`);
}

export interface Doc {
  href: string;
  title: string;
}

// docs are the terms the page links to their reference entries. Operations
// are keyed by their names.
export const docs = {
  lineage: { href: ref("lineage.md"), title: "Lineages" },
  versions: { href: ref("lineage.md", "versions"), title: "Versions, minor and major" },
  forms: { href: ref("lineage.md", "forms"), title: "The normal form and the authoring form" },
  bind: { href: ref("lineage.md", "binding"), title: "Binding: what thema checks of a lineage" },
  validate: { href: ref("operations.md", "validate"), title: "validate" },
  "validate-any": { href: ref("operations.md", "validate-any"), title: "validate-any" },
  translate: { href: ref("operations.md", "translate"), title: "translate" },
  hydrate: { href: ref("operations.md", "hydrate"), title: "hydrate" },
  dehydrate: { href: ref("operations.md", "dehydrate"), title: "dehydrate" },
  openapi: { href: ref("codegen.md", "openapi"), title: "Generating OpenAPI" },
  jsonschema: { href: ref("codegen.md", "json-schema"), title: "Generating JSON Schema" },
  typescript: { href: ref("codegen.md", "typescript"), title: "Generating TypeScript" },
  lacunas: { href: ref("lacunas.md"), title: "Lacunas" },
  diagnostics: { href: themaDoc("errors/README.md"), title: "Diagnostic codes" },
} satisfies Record<string, Doc>;

export type Topic = keyof typeof docs;

export function hasDoc(name: string): name is Topic {
  return Object.hasOwn(docs, name);
}

// lacunaTypeDoc is the reference entry for a lacuna type.
export function lacunaTypeDoc(type: string): Doc {
  return { href: ref("lacunas.md", type.toLowerCase()), title: `The ${type} lacuna type` };
}

// DocLink is the small ? beside a term, opening its reference entry.
export function DocLink(props: { topic: Topic } | { doc: Doc }) {
  const d: Doc = "doc" in props ? props.doc : docs[props.topic];
  return (
    <a class="doc-link" href={d.href} target="_blank" rel="noreferrer" title={d.title} aria-label={`Reference: ${d.title}`}>
      ?
    </a>
  );
}
