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

// The tip the tour writes: a minor version, and the line it breaks it with.
export const minorFile = `Schema: {
	firstfield:  string
	secondfield: int
	size:        "small" | "medium" | "large" | *"medium"
}
`;
export const minorDefault = ` | *"medium"`;

// The ForwardLens the tour adds once the tip is a major version, without the
// field it needs and then with it.
export const majorLens = `
ForwardLens: {
	v1_0: _
	tip: {}
}
`;
export const lensFix = `size: "medium"`;

export const steps: Step[] = [
  {
    id: "lineage",
    title: "A lineage",
    about:
      "thema keeps every version of a schema in one lineage, with lenses that carry data between versions, and checks that they all fit together. This tour takes one small lineage, ship, through all of it.",
    todo: ["From Example, under Authoring form, choose `ship`."],
    see: "Two versions under history, `history/0.0.thema` and `history/1.0.thema`, and the tip, `tip.thema`, open in the editor, with `binds: 0.0, 1.0 · tip (clean at 1.0)` above them. 1.0 adds a required field, `secondfield`, so it is a new major version, and its file carries a lens each way between 1.0 and 0.0. The tip is the same schema as 1.0: nothing has changed since 1.0 was cut.",
    doc: "lineage",
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
    doc: "validate",
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
    id: "history",
    title: "History is frozen",
    about:
      "A version under history/ has been cut: data written against it exists, so it never changes. Every change to the lineage is made in one file, the tip, which becomes the next version when it is cut.",
    todo: ["Open `history/1.0.thema`, under history (frozen) beside the file tabs, and try to type in it.", "Then open `tip.thema` again."],
    see: "The history file is read-only, and says so above the editor. The tip is the one file you can edit.",
    doc: "versions",
  },
  {
    id: "minor",
    title: "A minor version",
    about:
      "A change that every 1.0 instance survives is a minor version. It needs no lenses, because thema checks that it is backward compatible instead. Adding a field with a default is such a change.",
    todo: ["Replace the text of `tip.thema` with this."],
    snippets: [{ label: "tip.thema", text: minorFile }],
    see: "`binds: 0.0, 1.0, tip (cuts as 1.1)`. thema derived the tip's version from the change: once it is cut, it is 1.1. Until then it is provisional, and the version selectors call it the tip.",
    doc: "versions",
  },
  {
    id: "hydrate",
    title: "Hydrate and dehydrate",
    about: "Hydrate fills in what a schema's defaults imply; dehydrate takes out values that only repeat them, the form to store.",
    todo: ["Choose `hydrate`, with schema `tip (cuts as 1.1)`, and run it on the first document.", "Then `dehydrate`, the same schema, on the second."],
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
    todo: ["Choose `translate`, from `tip (cuts as 1.1)` to `0.0`, and run it."],
    snippets: [{ label: "data", text: data.v11Large }],
    see: "Two steps, each dropping a field: `size` going to 1.0, with a lacuna thema wrote itself since the tip has no lens, then `secondfield` going to 0.0.",
    result: json({ firstfield: "hello" }),
    doc: "translate",
  },
  {
    id: "codegen",
    title: "Code from schemas",
    about: "Each schema can be written out as TypeScript, OpenAPI or JSON Schema. These read only the lineage, so the data editor steps aside.",
    todo: ["Choose `typescript`, with schema `latest`, and run it. Try `openapi` too."],
    see: "The tip's schema as a TypeScript interface, with its defaults beside it.",
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
    id: "major",
    title: "A breaking change",
    about:
      "Without its default, `size` would be a new required field, which 1.0 data lacks. That is a breaking change, so the tip becomes a new major version, and a major version needs a lens from the one before.",
    todo: [`In \`tip.thema\`, delete \`${minorDefault.trim()}\` from the \`size\` line.`],
    see: "`binds: 0.0, 1.0, tip (cuts as 2.0)`, and a warning, `E1001`: the tip has no ForwardLens, and thema cannot derive one, since 1.0 data has nothing to put in `size`. The tip is still being worked on, so the lineage binds; translating into the tip fails until the lens is written.",
    doc: "versions",
  },
  {
    id: "lens-check",
    title: "Lenses are checked",
    about:
      "thema refuses a lens that could fail on some valid instance. In the tip's lenses, `tip` names the tip's instance, since it has no version number yet; `v1_0` names the 1.0 instance.",
    todo: ["Add this ForwardLens to the end of `tip.thema`.", `Then write \`${lensFix}\` between the braces after \`tip:\`.`],
    snippets: [{ label: "tip.thema", text: majorLens }],
    see: "First `E0346`: the ForwardLens produces nothing for `size`, which the tip requires, and the editor marks the lens. With `size` written, the warning is gone and the tip binds as 2.0.",
    doc: "bind",
  },
  {
    id: "forms",
    title: "Two forms",
    about:
      "The version files are the authoring form, written for people. thema compiles them to the normal form, one CUE file holding every schema and every lens, which is what every thema tool reads.",
    todo: ["Click Show as normal form, and back with Show as version files. The normal form tab below shows it too."],
    see: "The same lineage, with what thema implied written out: the copies, the lens back from the tip, and the lacunas it derived. The tip is in it as 2.0, the version it cuts as.",
    doc: "forms",
  },
  {
    id: "errors",
    title: "When it goes wrong",
    about: "thema's diagnostics say what is wrong, where, and usually what to write instead. The Errors examples each show one.",
    todo: ["From Example, under Errors, choose `optional-to-required`."],
    see: "`E0343`: a field optional in 0.0 is required in 1.0, and the message says what to write in the lens.",
    doc: "diagnostics",
  },
  {
    id: "share",
    title: "Share it",
    about: "Everything you have in the playground fits in a link.",
    todo: ["Click Share."],
    see: "The link is copied. It carries the lineage, the data and the chosen operation; nothing is stored anywhere else.",
  },
];
