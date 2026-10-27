// A small CUE highlighter for CodeMirror. CUE has no CodeMirror 6 language
// package; this tokenizes what an editor needs to colour: comments, strings,
// numbers, keywords, builtin types, definitions, attributes and punctuation.
// It does not parse.
import { StreamLanguage, type StreamParser } from "@codemirror/language";

const keywords = new Set(["package", "import", "for", "in", "if", "let", "true", "false", "null"]);
const types = new Set(["bool", "string", "bytes", "number", "int", "float", "uint", "rune", "_", "_|_", "int8", "int16", "int32", "int64", "uint8", "uint16", "uint32", "uint64", "float32", "float64"]);
// The authoring form's own words.
const authoring = new Set(["Schema", "ForwardLens", "BackwardLens", "lacunas", "drop", "orelse", "match", "each", "when"]);

interface State {
  // inString is the closing delimiter of a multi-line string being read.
  inString: string | null;
  inComment: boolean;
}

export const cueParser: StreamParser<State> = {
  name: "cue",
  startState: () => ({ inString: null, inComment: false }),
  token(stream, state) {
    if (state.inString) {
      const close = state.inString;
      while (!stream.eol()) {
        if (stream.match(close)) {
          state.inString = null;
          return "string";
        }
        if (stream.next() === "\\") stream.next();
      }
      return "string";
    }
    if (stream.eatSpace()) return null;
    if (stream.match("//")) {
      stream.skipToEnd();
      return "comment";
    }
    if (stream.match('"""') || stream.match("'''")) {
      state.inString = stream.current();
      return "string";
    }
    const ch = stream.peek();
    if (ch === '"' || ch === "'") {
      stream.next();
      while (!stream.eol()) {
        const c = stream.next();
        if (c === "\\") {
          stream.next();
        } else if (c === ch) {
          break;
        }
      }
      return "string";
    }
    if (ch === "@") {
      stream.next();
      stream.match(/^[A-Za-z_][\w]*/);
      return "meta";
    }
    if (stream.match(/^-?(0x[0-9a-fA-F_]+|0o[0-7_]+|0b[01_]+|\d[\d_]*(\.\d+)?([eE][+-]?\d+)?[KMGTP]?i?)/)) {
      return "number";
    }
    if (stream.match("_|_")) return "typeName";
    if (stream.match(/^#?[A-Za-z_$][\w$]*/)) {
      const w = stream.current();
      if (w.startsWith("#")) return "className";
      if (keywords.has(w)) return "keyword";
      if (types.has(w)) return "typeName";
      if (authoring.has(w)) return "keyword";
      return stream.peek() === ":" || stream.match(/^\s*[?!]?\s*:/, false) ? "propertyName" : "variableName";
    }
    if (stream.match(/^(\.\.\.|=~|!~|[=!<>]=?|&&|\|\||[&|*+\-/])/)) return "operator";
    stream.next();
    return "punctuation";
  },
  languageData: {
    commentTokens: { line: "//" },
    closeBrackets: { brackets: ["(", "[", "{", '"'] },
  },
};

export const cue = StreamLanguage.define(cueParser);
