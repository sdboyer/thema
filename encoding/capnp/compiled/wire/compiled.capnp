# The compiled-lineage artifact: what encoding/capnp produces from a thema
# lineage, as a Cap'n Proto message, for an engine that never links CUE to
# load (#90). It carries the lineage's compiled schema (as text and as the
# layout the capnp tool computed), the struct and enum nodes and how each maps
# back to the lineage's labels and values, the versions and the struct each is
# read through, every lens program (the lens IR) with its expressions rendered
# to CEL, the extras acceptance checks (#48), and what translation reads
# around each step for the fields a program does not write.
#
# Nothing here is specific to Go: the $Go annotations only direct Go code
# generation, and the artifact is the same message under any binding. The
# meta-schema is a published language (compiled-artifact in .thresh/thresh.cue):
# a field may be appended, never renumbered, retyped or removed.

@0xed252d9ad720985b;

using Go = import "/go.capnp";
$Go.package("wire");
$Go.import("github.com/sdboyer/thema/encoding/capnp/compiled/wire");

struct Artifact {
  # A lineage compiled to Cap'n Proto.

  namespace @0 :Text;
  # The hash namespace the type IDs were derived under.
  lineage @1 :Text;
  # The lineage's name.
  schema @2 :Text;
  # The .capnp text declaring every node.
  layout @3 :Data;
  # The CodeGeneratorRequest capnp compile -o- produced for schema; empty
  # where the compiler ran without the tool.
  versions @4 :List(Version);
  nodes @5 :List(Node);
  edges @6 :List(Edge);
  checks @7 :List(Check);
  messages @8 :List(AsideMessage);
  # The templates of the lacunas translation raises by itself, for the fields
  # no program writes and translation does not carry.
}

struct SynVer {
  # A syntactic version, major.minor.
  major @0 :UInt32;
  minor @1 :UInt32;
}

struct Version {
  # One schema version of the lineage.
  version @0 :SynVer;
  id @1 :Text;
  # The version's content-hash identity, <schema digest>:<lens digest>.
  root @2 :UInt64;
  # The ID of the struct node instances of this version are read through.
  structs @3 :List(StructShape);
  # The structs the version's schema declares, at any depth, root first.
}

struct StructShape {
  # One struct a version's schema declares: what translation reads of it to
  # decide whether undeclared data has a place there.
  path @0 :List(Step);
  # The struct's path in an instance; a list's element and a map's value are
  # any. The struct alternatives of one disjunction share a path.
  node @1 :UInt64;
  # The struct node it is read through.
  labels @2 :List(Text);
  # The labels this version declares in the struct, in declaration order.
  open @3 :Bool;
  # The author left the struct open (...): it carries undeclared data.
}

struct Node {
  # One struct or enum node of the schema.
  id @0 :UInt64;
  name @1 :Text;
  # The display name: the root struct's name, then each nesting level joined
  # with ".".
  kind @2 :NodeKind;
  role @3 :NodeRole;
  fields @4 :List(Field);
  enumerants @5 :List(Enumerant);
}

enum NodeKind {
  structNode @0;
  enumNode @1;
}

enum NodeRole {
  schemaStruct @0;
  # A struct the schema declares; it carries extras.
  mapEntry @1;
  # An entry of a [string]: T field: key @0 :Text, value @1 :T.
  unionBox @2;
  # A disjunction where capnp has no union (a list element, a map value); its
  # only field is an anonymous union.
}

struct Field {
  # One field of a struct node.
  label @0 :Text;
  # The schema's label; "" for a field the compiler adds.
  name @1 :Text;
  # The capnp name.
  form @2 :Form;
  type @3 :Type;
  # The field's type where it is plain; null where it is a union.
  ordinal @4 :UInt16;
  union @5 :List(Member);
  # The union's members where the field is optional, a discriminated
  # disjunction, or both.
  default @6 :Literal;
  # The schema's default where the field is defaulted and the default is a
  # scalar; null otherwise.
}

enum Form {
  required @0;
  optional @1;
  defaulted @2;
}

struct Member {
  # One member of a field's union.
  name @0 :Text;
  role @1 :MemberRole;
  ordinal @2 :UInt16;
  type @3 :Type;
  # null for a Void member.
  kind @4 :Text;
  # The alternative's kind for a kindMember.
  tagLabel @5 :Text;
  tag @6 :Literal;
  # The discriminating label and this alternative's constant for a tagMember.
}

enum MemberRole {
  unset @0;
  value @1;
  kindMember @2;
  tagMember @3;
}

struct Type {
  # A capnp type: a scalar, a list of an element type, or a reference to an
  # enum or struct node.
  kind @0 :TypeKind;
  elem @1 :Type;
  node @2 :UInt64;
}

enum TypeKind {
  void @0;
  bool @1;
  int8 @2;
  int16 @3;
  int32 @4;
  int64 @5;
  uint8 @6;
  uint16 @7;
  uint32 @8;
  uint64 @9;
  text @10;
  data @11;
  list @12;
  enumRef @13;
  structRef @14;
}

struct Enumerant {
  name @0 :Text;
  value @1 :Text;
  # The string the lineage declares.
  ordinal @2 :UInt16;
}

struct Literal {
  # A scalar value.
  kind @0 :LiteralKind;
  bool @1 :Bool;
  int @2 :Text;
  # The decimal digits of an int.
  float @3 :Text;
  # A float as CUE prints it.
  str @4 :Text;
  # The text of a string.
  bytes @5 :Data;
  # The bytes of a bytes literal.
}

enum LiteralKind {
  null @0;
  bool @1;
  int @2;
  float @3;
  string @4;
  bytes @5;
}

struct Edge {
  # One step a translation runs a program for.
  from @0 :SynVer;
  to @1 :SynVer;
  synthesized @2 :Bool;
  # The program is one thema derived (the identity copy of a forward minor
  # step across a change of struct), not one compiled from a lens.
  result @3 :List(IRNode);
  # The nodes writing the result, in the order the lens declares them.
  lacunas @4 :List(Lacuna);
  cel @5 :List(Cel);
  # The CEL rendering of each expression of the program.
  unions @6 :List(UnionWrite);
  # The writes of the program that name an alternative of a disjunction
  # (a copy's alts, a wrap's alt), lowered to members of the compiled unions.
  fate @7 :Fate;
  # What translation reads of the step's lens to decide the fate of the
  # fields its program does not write. Null for a step with no lens.
}

struct Fate {
  # What translation reads of a lens, from the lens itself, for the fields its
  # program does not write. Paths are within the lens's input or result.
  reads @0 :List(PathRef);
  # The input paths the lens's result reads.
  readers @1 :List(PathPair);
  # Each read input path, with a result field that copies or computes from it.
  copies @2 :List(PathPair);
  # Each result field whose contents come whole from one input value.
  named @3 :List(Named);
  # The input paths the lens's lacunas name in their sourceFields.
}

struct PathRef {
  steps @0 :List(Step);
}

struct PathPair {
  result @0 :List(Step);
  input @1 :List(Step);
}

struct Named {
  # A source path a lacuna of the lens names, as the program's field reference
  # field of lacuna lacuna does, under that reference's guard.
  path @0 :List(Step);
  lacuna @1 :UInt32;
  field @2 :UInt32;
}

struct AsideMessage {
  # The template of the message of a lacuna translation raises by itself.
  # Placeholders: {fields}, {field}, {target}, {from}, {to}, {struct}.
  cause @0 :AsideCause;
  form @1 :MessageForm;
  type @2 :Text;
  # The lacuna's type: DroppedField or LossyFieldMapping.
  text @3 :Text;
}

enum AsideCause {
  closed @0;
  # The target does not declare the field, and its struct there is closed.
  nowhere @1;
  # The result has no struct at the field's parent path.
  written @2;
  # The target declares the field, and the program wrote its own value.
  unwritten @3;
  # Both versions declare the field, and the lens neither maps nor writes it.
  lost @4;
  # The lens reads the field, none of its readers was written, and it could
  # not be carried.
  lossy @5;
  # The value is not an instance of the field the target declares for it.
}

enum MessageForm {
  one @0;
  several @1;
  moved @2;
}

struct UnionWrite {
  # One copy or wrap of a program, whose IR names alternatives of a
  # disjunction, lowered to the members of the unions that hold them.
  node @0 :UInt32;
  # The copy or wrap: its index in a pre-order walk of the program's result,
  # nested nodes counted where they appear (as Cel.node).
  source @1 :List(UInt16);
  # For a copy, per entry of its alts in order: the ordinal of the member of
  # the source field's union holding the alternative copied from. Empty where
  # the source is not a union, and for a wrap.
  target @2 :List(UInt16);
  # For a copy, per entry of its alts in order: the ordinal of the member of
  # the target field's union to set. For a wrap, the one member it writes.
}

struct Alt {
  # A source alternative of a copy and the target alternative accepting it,
  # numbered as the schemas declare them at the path (internal/lensir Alt).
  from @0 :UInt32;
  to @1 :UInt32;
}

struct Step {
  # One step of a path within an instance.
  union {
    label @0 :Text;
    index @1 :UInt32;
    any @2 :Void;
    # The open element of a list, inside an each.
  }
}

struct IRNode {
  # One thing a lens writes (internal/lensir).
  union {
    copy :group {
      to @0 :List(Step);
      from @1 :List(Step);
      alts @22 :List(Alt);
      # Where the target is a disjunction with a struct or list alternative:
      # the target alternative each copied source alternative lands in.
    }
    constant :group {
      to @2 :List(Step);
      value @3 :Literal;
    }
    wrap :group {
      to @4 :List(Step);
      kind @5 :WrapKind;
      len @6 :UInt32;
      children @7 :List(IRNode);
      alt @23 :Int32 = -1;
      # Where the target is a disjunction: the alternative the literal is an
      # instance of; -1 where none is named.
    }
    each :group {
      to @8 :List(Step);
      source @9 :List(Step);
      body @10 :List(IRNode);
    }
    branch :group {
      when @11 :List(Test);
      then @12 :List(IRNode);
    }
    head :group {
      to @13 :List(Step);
      when @14 :List(Test);
      then @15 :List(IRNode);
      else @16 :List(IRNode);
    }
    table :group {
      to @17 :List(Step);
      key @18 :List(Step);
      entries @19 :List(Entry);
    }
    computed :group {
      to @20 :List(Step);
      expr @21 :Expr;
    }
  }
}

enum WrapKind {
  structWrap @0;
  listWrap @1;
}

struct Entry {
  # One proven entry of a table.
  key @0 :Text;
  value @1 :List(IRNode);
}

struct Test {
  # One pattern a lens tests the input with.
  kind @0 :TestKind;
  path @1 :List(Step);
  neg @2 :Bool;
  value @3 :Literal;
  # The literal an equality compares with.
  type @4 :Text;
  # The constraint a typeTest unifies with, as the lens wrote it.
  safe @5 :Bool;
}

enum TestKind {
  presence @0;
  equality @1;
  typeTest @2;
}

struct Expr {
  # An expression a computed node evaluates, or a lacuna reports.
  union {
    literal @0 :Literal;
    ref @1 :List(Step);
    # The input at the path.
    resultRef @2 :List(Step);
    # The result at the path.
    unary :group {
      op @3 :Text;
      x @4 :Expr;
    }
    binary :group {
      op @5 :Text;
      x @6 :Expr;
      y @7 :Expr;
    }
    interp @8 :List(Expr);
    call :group {
      fn @9 :Text;
      args @10 :List(Expr);
    }
    match @11 :List(Test);
    cond :group {
      when @12 :List(Test);
      then @13 :Expr;
      else @14 :Expr;
    }
  }
}

struct Lacuna {
  # One lacuna the lens declares.
  when @0 :Expr;
  # null where the lacuna always applies.
  sourceFields @1 :List(FieldRef);
  targetFields @2 :List(FieldRef);
  msg @3 :Expr;
  # The message reported: a string literal, or an expression building one
  # (named msg because every generated binding has a message accessor).
  type @4 :Text;
  # The lacuna type, as thema names it (LossyFieldMapping, ...).
}

struct FieldRef {
  # A field a lacuna reports.
  path @0 :Text;
  when @1 :List(Test);
  value @2 :Expr;
}

struct Cel {
  # The CEL rendering of one expression of a program.
  role @0 :CelRole;
  node @1 :UInt32;
  # For computedValue: the index of the computed node in a pre-order walk of
  # the program's result, nested nodes counted where they appear.
  lacuna @2 :UInt32;
  # For the lacuna roles: the index of the lacuna.
  field @3 :UInt32;
  # For the field roles: the index of the field reference.
  source @4 :Text;
  # The CEL expression. It reads the variables in vars and nothing else.
  vars @5 :List(Var);
}

enum CelRole {
  computedValue @0;
  lacunaWhen @1;
  lacunaMessage @2;
  lacunaSourceField @3;
  lacunaTargetField @4;
}

struct Var {
  # A variable a CEL expression reads.
  name @0 :Text;
  path @1 :List(Step);
  role @2 :VarRole;
  type @3 :Text;
  # The variable's CEL type.
}

enum VarRole {
  value @0;
  # The value of the input at path.
  present @1;
  # Whether the input has a value at path.
  which @2;
  # The name of the union member the input holds at path.
  result @3;
  # The value of the result at path.
}

struct Check {
  # One acceptance check: whether a value arriving as undeclared data under
  # label is an instance of the field the version declares at the struct
  # node.
  version @0 :SynVer;
  node @1 :UInt64;
  label @2 :Text;
  source @3 :Text;
  # A CEL expression over the variable x, the extra's value, typed dyn.
}
