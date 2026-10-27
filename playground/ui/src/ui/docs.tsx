// Where the page points for what thema's terms mean: thema's own docs at the
// commit thema.version pins, so they describe the thema the page runs, and
// pkg.go.dev for what only the Go API documents.
import pin from "../../thema.version?raw";

export const themaCommit = pin.trim();

// themaDoc is the URL of a file under thema's docs/ at the pinned commit.
export function themaDoc(path: string): string {
  return `https://github.com/sdboyer/thema/blob/${themaCommit}/docs/${path}`;
}

// goDoc is a package's page on pkg.go.dev. pkg.go.dev resolves versions, not
// commits, so this is the latest; the Go API it describes changes slowly.
function goDoc(pkg: string, symbol = ""): string {
  return `https://pkg.go.dev/github.com/sdboyer/thema${pkg ? "/" + pkg : ""}${symbol ? "#" + symbol : ""}`;
}

export interface Doc {
  href: string;
  title: string;
}

// docs are the terms the page links to an explanation of. Operations are
// keyed by their names.
export const docs = {
  bind: { href: themaDoc("invariants.md"), title: "What binding checks of a lineage" },
  forms: { href: themaDoc("authoring-form.md"), title: "The authoring form, and the normal form it compiles to" },
  lacunas: { href: themaDoc("authoring-form.md#lacunas"), title: "Lacunas: what a lens reports it could not carry over exactly" },
  "validate-any": { href: themaDoc("go-usage.md#search-by-validity"), title: "Finding the schemas data is an instance of" },
  translate: { href: themaDoc("go-usage.md#translate"), title: "Translating an instance to another schema, through the lenses" },
  hydrate: { href: goDoc("", "Instance.Hydrate"), title: "Hydrate: fill in the schema's defaults" },
  dehydrate: { href: goDoc("", "Instance.Dehydrate"), title: "Dehydrate: remove values equal to the schema's defaults" },
  openapi: { href: goDoc("encoding/openapi"), title: "Generating OpenAPI from a schema" },
  jsonschema: { href: goDoc("encoding/jsonschema"), title: "Generating JSON Schema from a schema" },
  typescript: { href: goDoc("encoding/typescript"), title: "Generating TypeScript from a schema" },
} satisfies Record<string, Doc>;

export type Topic = keyof typeof docs;

export function hasDoc(name: string): name is Topic {
  return Object.hasOwn(docs, name);
}

// DocLink is the small ? beside a term, opening its explanation.
export function DocLink({ topic }: { topic: Topic }) {
  const d: Doc = docs[topic];
  return (
    <a class="doc-link" href={d.href} target="_blank" rel="noreferrer" title={d.title} aria-label={`About: ${d.title}`}>
      ?
    </a>
  );
}
