// The tour: a walk through what thema does, on one lineage, as steps a
// reader follows by hand in the playground. Text in `backticks` is code.
//
// Each step's claims about thema's answers are held to by steps.wasm.test.ts,
// against the real thema: the data to paste, the codes named, and the
// results shown.
import type { Topic } from "../ui/docs";

export interface Snippet {
  // label says where the snippet goes.
  label: string;
  text: string;
}

export interface Step {
  id: string;
  title: string;
  // What the step is about, then what to do; one paragraph each.
  about: string;
  todo: string[];
  snippets?: Snippet[];
  // What the reader should then see, and the result itself where it helps.
  see: string;
  result?: string;
  doc?: Topic;
}

const json = (v: unknown) => JSON.stringify(v, null, 2) + "\n";

// The data the steps use.
export const data = {
  v1: json({ firstfield: "hello", secondfield: 42 }),
  v0: json({ firstfield: "hello" }),
  v11Bare: json({ firstfield: "hello", secondfield: 42 }),
  v11Default: json({ firstfield: "hello", secondfield: 42, size: "medium" }),
  v11Large: json({ firstfield: "hello", secondfield: 42, size: "large" }),
};

// The minor version the tour adds, and the line it breaks it with.
export const minorFile = `Schema: {
	firstfield:  string
	secondfield: int
	size:        "small" | "medium" | "large" | *"medium"
}
`;
export const minorDefault = ` | *"medium"`;

// The line of 1.0.thema the tour deletes, to see the lens checked.
export const lensLine = "secondfield: -1";

export const steps: Step[] = [
  {
    id: "lineage",
    title: "A lineage",
    about:
      "thema keeps every version of a schema in one lineage, with lenses that carry data between versions, and checks that they all fit together. This tour takes one small lineage, ship, through all of it.",
    todo: ["From Example, under Authoring form, choose `ship`."],
    see: "Two version files, `1.0.thema` and `history/0.0.thema`, and `binds: 0.0, 1.0` above them. 1.0 adds a required field, `secondfield`, so it is a new major version, and its file carries a lens each way between 1.0 and 0.0.",
    doc: "forms",
  },
  {
    id: "validate",
    title: "Validate",
    about: "Validation asks whether some data is an instance of one schema.",
    todo: ["Choose `validate`, with schema `latest`, and put this in the data editor. Run it.", "Then try the second document, which lacks `secondfield`."],
    snippets: [
      { label: "an instance of 1.0", text: data.v1 },
      { label: "not one", text: data.v0 },
    ],
    see: "The first is an instance of schema 1.0. The second fails with `E0501`, naming the missing field; the code links to its explanation.",
  },
  {
    id: "validate-any",
    title: "Which version is this?",
    about: "Data rarely says which version it was written against. validate-any finds the newest schema that accepts it.",
    todo: ["Choose `validate-any` and run it on the document without `secondfield`."],
    snippets: [{ label: "data", text: data.v0 }],
    see: "The newest schema it is an instance of is 0.0. Open schema 1.0 to see why that one refuses it.",
    doc: "validate-any",
  },
  {
    id: "translate-forward",
    title: "Translate forward",
    about:
      "Translation carries an instance from one version to another through the lenses. Where a lens cannot carry something over exactly, it says so with a lacuna, and the program decides what to do about it.",
    todo: ["Choose `translate`, from `the data's` to `1.0`, and run it on the 0.0 document."],
    snippets: [{ label: "data", text: data.v0 }],
    see: "Translated from 0.0 to 1.0 with one lacuna: 0.0 had nothing for `secondfield`, so the lens put in -1 and flagged it as a `Placeholder`.",
    result: json({ firstfield: "hello", secondfield: -1 }),
    doc: "translate",
  },
  {
    id: "translate-back",
    title: "And back",
    about: "Lenses go both ways, so a program written against 0.0 can still read 1.0 data.",
    todo: ["Translate from `1.0` to `0.0`, on the 1.0 document."],
    snippets: [{ label: "data", text: data.v1 }],
    see: "0.0 has nowhere to keep `secondfield`, so it is dropped, and a `DroppedField` lacuna says what was lost.",
    result: json({ firstfield: "hello" }),
    doc: "lacunas",
  },
  {
    id: "lens-check",
    title: "Lenses are checked",
    about: "thema refuses a lineage whose lenses could fail on some valid instance. It checks this as you type.",
    todo: [`In \`1.0.thema\`, delete the line \`${lensLine}\` from the ForwardLens.`, "Then undo, with Cmd-Z or Ctrl-Z."],
    see: "The lineage stops binding: `E0346` says the ForwardLens produces nothing for `secondfield`, which 1.0 requires, and the editor marks the line. After the undo it binds again.",
    doc: "bind",
  },
  {
    id: "minor",
    title: "A minor version",
    about:
      "A change that every 1.0 instance survives is a minor version: 1.1. It needs no lenses, because thema checks that it is backward compatible instead. Adding a field with a default is such a change.",
    todo: ["Click + beside the file tabs, which adds `1.1.thema`, and replace its text with this."],
    snippets: [{ label: "1.1.thema", text: minorFile }],
    see: "`binds: 0.0, 1.0, 1.1`.",
    doc: "bind",
  },
  {
    id: "compat-check",
    title: "Compatibility is checked",
    about: "Without its default, `size` would be a new required field, which 1.0 data lacks. That is a breaking change, not a minor one.",
    todo: [`In \`1.1.thema\`, delete \`${minorDefault.trim()}\` from the \`size\` line.`, "Then undo."],
    see: "`E0210`: `size` is required in the new schema but not declared in the prior one. After the undo it binds again.",
    doc: "bind",
  },
  {
    id: "hydrate",
    title: "Hydrate and dehydrate",
    about: "Hydrate fills in what a schema's defaults imply; dehydrate takes out values that only repeat them, the form to store.",
    todo: ["Choose `hydrate`, with schema `1.1`, and run it on the first document.", "Then `dehydrate`, schema `1.1`, on the second."],
    snippets: [
      { label: "to hydrate", text: data.v11Bare },
      { label: "to dehydrate", text: data.v11Default },
    ],
    see: "Hydrate adds `size: \"medium\"`; dehydrate removes it again.",
    result: json({ firstfield: "hello", secondfield: 42, size: "medium" }),
    doc: "hydrate",
  },
  {
    id: "translate-chain",
    title: "Translation chains",
    about: "Between versions further apart, thema runs each lens in turn, and keeps the lacunas of every step.",
    todo: ["Choose `translate`, from `1.1` to `0.0`, and run it."],
    snippets: [{ label: "data", text: data.v11Large }],
    see: "Two steps, each dropping a field: `size` going to 1.0, with a lacuna thema wrote itself since 1.1 has no lens, then `secondfield` going to 0.0.",
    result: json({ firstfield: "hello" }),
    doc: "translate",
  },
  {
    id: "codegen",
    title: "Code from schemas",
    about: "Each schema can be written out as TypeScript, OpenAPI or JSON Schema. These read only the lineage, so the data editor steps aside.",
    todo: ["Choose `typescript`, with schema `latest`, and run it. Try `openapi` too."],
    see: "The 1.1 schema as a TypeScript interface, with its defaults beside it.",
    result: `export interface Ship {
  firstfield: string;
  secondfield: number;
  size: ('small' | 'medium' | 'large');
}

export const defaultShip: Partial<Ship> = {
  size: 'medium',
};
`,
    doc: "typescript",
  },
  {
    id: "forms",
    title: "Two forms",
    about:
      "The version files are the authoring form, written for people. thema compiles them to the normal form, one CUE file holding every schema and every lens, which is what every thema tool reads.",
    todo: ["Click Show as normal form, and back with Show as version files. The normal form tab below shows it too."],
    see: "The same lineage, with what thema implied written out: the copies, the lens back from 1.1, and the lacunas it derived.",
    doc: "forms",
  },
  {
    id: "errors",
    title: "When it goes wrong",
    about: "thema's diagnostics say what is wrong, where, and usually what to write instead. The Errors examples each show one.",
    todo: ["From Example, under Errors, choose `optional-to-required`."],
    see: "`E0343`: a field optional in 0.0 is required in 1.0, and the message says what to write in the lens.",
  },
  {
    id: "share",
    title: "Share it",
    about: "Everything you have in the playground fits in a link.",
    todo: ["Click Share."],
    see: "The link is copied. It carries the lineage, the data and the chosen operation; nothing is stored anywhere else.",
  },
];
