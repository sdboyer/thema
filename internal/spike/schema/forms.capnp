@0xd3b2a1c4e5f60718;

# 0.0: {a: int32, t: string}, both required.
struct Old {
  a @0 :Int32;
  t @1 :Text;
}

# (a) minor appends opt?: int  -> union, unset first (discriminant 0 = unset).
struct AppendOptional {
  a @0 :Int32;
  t @1 :Text;
  opt :union {
    unset @2 :Void;
    value @3 :Int64;
  }
}

# (b)(c) minor appends d: *7 | int and s: *"foo" | string.
struct AppendDefaulted {
  a @0 :Int32;
  t @1 :Text;
  d @2 :Int64 = 7;
  s @3 :Text = "foo";
}

# (d) a: required -> optional. Moved into a new union with the original field
# first, so old bytes (discriminant 0) read as "value".
struct RequiredToOptional {
  a :union {
    value @0 :Int32;
    unset @2 :Void;
  }
  t @1 :Text;
}

# (d') the #82 shape, unset first, applied to a field that used to be required.
struct RequiredToOptionalUnsetFirst {
  a :union {
    unset @2 :Void;
    value @0 :Int32;
  }
  t @1 :Text;
}

# (e) a: required -> defaulted 7, by declaring a capnp default on the data field.
struct RequiredToDefaultedData {
  a @0 :Int32 = 7;
  t @1 :Text;
}

# (f) t: required -> defaulted "foo", on a pointer field.
struct RequiredToDefaultedPtr {
  a @0 :Int32;
  t @1 :Text = "foo";
}

# (g) a widened int32 -> int64: not append-compatible in capnp.
struct Widened {
  a @0 :Int64;
  t @1 :Text;
}

# A nested struct copied across layouts (Q2 deep copy).
struct Outer0 {
  inner @0 :Old;
}
struct Outer1 {
  inner @0 :AppendDefaulted;
}
