"use strict";

const fs = require("node:fs");
const path = require("node:path");
const root = path.resolve(__dirname, "..");
const reference = require(path.join(root, ".reference/WMPFDebugger-2b90b77/src/third-party/WARemoteDebugProtobuf.js"));
const namespace = reference.mmbizwxadevremote;

function valid(Type, field, value) {
  return Type.verify({ [field]: value }) === null;
}

function sample(Type) {
  const defaults = Type.toObject(Type.create(), { defaults: true, arrays: true, longs: Number, bytes: Array });
  const value = {};
  for (const [field, current] of Object.entries(defaults)) {
    if (Array.isArray(current)) {
      if (valid(Type, field, [field + "-value"])) value[field] = [field + "-value", field + "-value-2"];
      else if (valid(Type, field, [{}])) value[field] = [{}, {}];
      continue;
    }
    if (typeof current === "string") value[field] = field + "-value";
    else if (typeof current === "boolean") value[field] = true;
    else if (typeof current === "number") value[field] = field.includes("width") || field.includes("ratio") ? 7.5 : 7;
    else if (current === null && valid(Type, field, {})) value[field] = {};
    else if (valid(Type, field, 7)) value[field] = 7;
  }
  const error = Type.verify(value);
  if (error !== null) throw new Error(`${Type.name}: ${error}`);
  return value;
}

function explicitZero(Type) {
  const defaults = Type.toObject(Type.create(), { defaults: true, arrays: true, longs: Number, bytes: Array });
  const value = {};
  for (const [field, current] of Object.entries(defaults)) {
    if (Array.isArray(current)) value[field] = [];
    else if (typeof current === "string") value[field] = "";
    else if (typeof current === "boolean") value[field] = false;
    else if (typeof current === "number") value[field] = 0;
    else if (current === null && valid(Type, field, {})) value[field] = {};
    else if (valid(Type, field, 0)) value[field] = 0;
  }
  const error = Type.verify(value);
  if (error !== null) throw new Error(`${Type.name} zero: ${error}`);
  return value;
}

function encodeCase(Type, input) {
  const encoded = Type.encode(Type.create(input)).finish();
  const decoded = Type.toObject(Type.decode(encoded), {
    defaults: true,
    arrays: true,
    objects: true,
    longs: String,
    bytes: Array,
  });
  return { input, protobuf_hex: Buffer.from(encoded).toString("hex"), decoded };
}

function fieldValue(field, value) {
  if (value != null) return value;
  if (field === "baseRequest") return {};
  if (field === "baseResponse") return {};
  if (field === "roomInfo") return {};
  if (field === "registerInterface") return {};
  if (field === "deviceInfo") return {};
  return value;
}

const fixtures = Object.keys(namespace)
  .filter((name) => name.startsWith("WARemoteDebug_"))
  .sort()
  .map((type) => {
    const Type = namespace[type];
    const value = sample(Type);
    const populated = encodeCase(Type, value);
    const zero = encodeCase(Type, explicitZero(Type));
    const defaults = Type.toObject(Type.create(), { defaults: true, arrays: true, objects: true, longs: Number, bytes: Array });
    const fields = Object.keys(defaults).map((field) => {
      try {
        const input = { [field]: fieldValue(field, value[field]) };
        if (Object.hasOwn(defaults, "baseRequest") && field !== "baseRequest") input.baseRequest = {};
        if (Object.hasOwn(defaults, "baseResponse") && field !== "baseResponse") input.baseResponse = {};
        return { field, ...encodeCase(Type, input) };
      } catch (error) {
        throw new Error(`${type}.${field}: ${error.message}`);
      }
    });
    return { type, ...populated, explicit_zero: zero, fields };
  });

if (fixtures.length !== 55) throw new Error(`expected 55 messages, got ${fixtures.length}`);
const duplicateSetup = Buffer.from("0a070a0566697273740a00", "hex");
const duplicateDecoded = namespace.WARemoteDebug_SetupContext.toObject(
  namespace.WARemoteDebug_SetupContext.decode(duplicateSetup),
  { defaults: true, arrays: true, objects: true, longs: String, bytes: Array },
);
const corruptInputs = [
  ["truncated_varint", "08"],
  ["overlong_varint", "08ffffffffffffffffffff01"],
  ["truncated_bytes", "120501"],
  ["truncated_fixed32_unknown", "fd050102"],
  ["truncated_fixed64_unknown", "f905010203"],
  ["invalid_wire_type_7", "ff05"],
].map(([name, protobuf_hex]) => {
  let error = null;
  let decoded = null;
  try {
    decoded = namespace.WARemoteDebug_Ping.toObject(
      namespace.WARemoteDebug_Ping.decode(Buffer.from(protobuf_hex, "hex")),
      { defaults: true, arrays: true, objects: true, longs: String, bytes: Array },
    );
  }
  catch (caught) { error = String(caught && caught.message || caught); }
  return { name, protobuf_hex, error, decoded };
});

const output = path.join(root, "testdata/golden/reference_messages.json");
fs.writeFileSync(output, JSON.stringify({
  reference_commit: "2b90b77fc6f13dd18480cd07d7dd9c052cc26c9d",
  fixtures,
  duplicate_singular: { type: "WARemoteDebug_SetupContext", protobuf_hex: duplicateSetup.toString("hex"), decoded: duplicateDecoded },
  corrupt_inputs: corruptInputs,
}, null, 2) + "\n");
console.log(`${output}: ${fixtures.length} fixtures`);
