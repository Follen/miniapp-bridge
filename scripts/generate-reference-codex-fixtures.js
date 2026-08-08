"use strict";

const fs = require("node:fs");
const path = require("node:path");

const root = path.resolve(__dirname, "..");
const ref = path.join(root, ".reference/WMPFDebugger-2b90b77/src/third-party");
const codex = require(path.join(ref, "RemoteDebugCodex.js"));
const constants = require(path.join(ref, "RemoteDebugConstants.js"));
const protobuf = require(path.join(ref, "WARemoteDebugProtobuf.js")).mmbizwxadevremote;

const hex = (value) => Buffer.from(value || []).toString("hex");
const encode = (Type, value) => Type.encode(Type.create(value)).finish();
const dataFormat = (cmd, uuid, Type, value) => encode(protobuf.WARemoteDebug_DataFormat, {
  cmd,
  uuid,
  data: encode(Type, value),
});

function caught(fn) {
  try {
    return { error: null, value: fn() };
  } catch (error) {
    return { error: String(error && error.message || error), value: null };
  }
}

const debugInputs = [
  { name: "ping_snake", category: constants.DebugMessageCategory.Ping, data: { ping_id: 900719925, payload: "ping-payload" }, compress_algo: 0 },
  { name: "ping_zlib", category: constants.DebugMessageCategory.Ping, data: { ping_id: 77, payload: "compress-me-".repeat(24) }, compress_algo: constants.CompressAlgo.Zlib },
  { name: "ping_zlib_bitmask", category: constants.DebugMessageCategory.Ping, data: { ping_id: 78, payload: "bitmask-".repeat(32) }, compress_algo: constants.CompressAlgo.Zlib | 2 },
  { name: "call_interface_stringifies_args", category: constants.DebugMessageCategory.CallInterface, data: { name: "wx", method: "invoke", args: [7, true, null, "raw"], call_id: 41 }, compress_algo: 0 },
  { name: "breakpoint_boolean", category: constants.DebugMessageCategory.Breakpoint, data: { is_hit: 2 }, compress_algo: 0 },
  { name: "chrome_snake", category: constants.DebugMessageCategory.ChromeDevtools, data: { op_id: 55, payload: "{\"id\":55}", jscontext_id: "ctx-main" }, compress_algo: 0 },
  { name: "custom_raw", category: constants.DebugMessageCategory.CustomMessage, data: { method: "notice", payload: "{}", raw: Buffer.from([0, 1, 254, 255]) }, compress_algo: 0 },
];

const allCategoryInputs = [
  ["setup", C => C.SetupContext, protobuf.WARemoteDebug_SetupContext, { registerInterface: { objName: "wx", objMethodList: [{ methodName: "m", methodArgList: ["a"] }] }, deviceInfo: { deviceName: "d", deviceModel: "m", systemVersion: "s", wechatVersion: "w", publibVersion: 1, screenWidth: 2.5, pixelRatio: 3, userAgent: "u" }, configureJs: "c", publicJsMd5: "p", threeJsMd5: "t", supportCompressAlgo: 1 }, {}],
  ["call", C => C.CallInterface, protobuf.WARemoteDebug_CallInterface, { objName: "wx", methodName: "m", methodArgList: ["a"], callId: 1 }, { name: "wx", method: "m", args: ["a"], call_id: 1 }],
  ["call_result", C => C.CallInterfaceResult, protobuf.WARemoteDebug_CallInterfaceResult, { ret: "r", callId: 2, debugInfo: "d" }, {}],
  ["evaluate", C => C.EvaluateJavascript, protobuf.WARemoteDebug_EvaluateJavascript, { script: "1", evaluateId: 3, debugInfo: "d" }, {}],
  ["evaluate_result", C => C.EvaluateJavascriptResult, protobuf.WARemoteDebug_EvaluateJavascriptResult, { ret: "1", evaluateId: 3 }, { ret: "1", evaluate_id: 3 }],
  ["breakpoint", C => C.Breakpoint, protobuf.WARemoteDebug_Breakpoint, { isHit: false }, { is_hit: 0 }],
  ["ping", C => C.Ping, protobuf.WARemoteDebug_Ping, { pingId: 4, payload: "p" }, { ping_id: 4, payload: "p" }],
  ["pong", C => C.Pong, protobuf.WARemoteDebug_Pong, { pingId: 4, networkType: 5, payload: "p" }, {}],
  ["dom_op", C => C.DomOp, protobuf.WARemoteDebug_DomOp, { params: "{}", webviewId: 6 }, { params: "{}", webview_id: 6 }],
  ["dom_event", C => C.DomEvent, protobuf.WARemoteDebug_DomEvent, { params: "{}", webviewId: 7 }, { params: "{}", webview_id: 7 }],
  ["network", C => C.NetworkDebugAPI, protobuf.WARemoteDebug_NetworkDebugAPI, { apiName: "a", taskId: "t", requestHeaders: "h", timestamp: 8 }, {}],
  ["chrome", C => C.ChromeDevtools, protobuf.WARemoteDebug_ChromeDevtools, { opId: 9, payload: "{}", jscontextId: "c" }, { op_id: 9, payload: "{}", jscontext_id: "c" }],
  ["chrome_result", C => C.ChromeDevtoolsResult, protobuf.WARemoteDebug_ChromeDevtoolsResult, { opId: 10, payload: "{}", jscontextId: "c" }, {}],
  ["add_context", C => C.AddJsContext, protobuf.WARemoteDebug_AddJsContext, { jscontextId: "a", jscontextName: "n" }, {}],
  ["remove_context", C => C.RemoveJsContext, protobuf.WARemoteDebug_RemoveJsContext, { jscontextId: "r" }, {}],
  ["connect_context", C => C.ConnectJsContext, protobuf.WARemoteDebug_ConnectJsContext, { jscontextId: "c" }, { jscontext_id: "c" }],
  ["engine_event", C => C.EngineEvent, null, null, {}],
  ["engine_op", C => C.EngineOp, null, null, {}],
  ["custom", C => C.CustomMessage, protobuf.WARemoteDebug_CustomMessage, { method: "m", payload: "{}", raw: Buffer.from([1, 2]) }, { method: "m", payload: "{}", raw: Buffer.from([1, 2]) }],
];

const categoryBranches = allCategoryInputs.map(([name, categoryOf, Type, protoValue, wrapValue]) => {
  const category = categoryOf(constants.DebugMessageCategory);
  const payload = Type ? encode(Type, protoValue) : Buffer.from([8, 1]);
  const proto = { seq: 21, after: 22, category, data: payload, compressAlgo: 0, originalSize: 0 };
  const wrapped = caught(() => codex.wrapDebugMessageData(wrapValue, category, 0));
  return {
    name,
    category,
    payload_hex: hex(payload),
    unwrap: codex.unwrapDebugMessageData(proto),
    wrap_payload_hex: wrapped.value ? hex(wrapped.value.buffer) : null,
    wrap_original_size: wrapped.value ? wrapped.value.originalSize : null,
    wrap_error: wrapped.error,
  };
});

codex.model.resetStatistics();
const debugWrap = debugInputs.map((item) => {
  const wrapped = codex.wrapDebugMessageData(item.data, item.category, item.compress_algo);
  const proto = {
    seq: 12,
    after: 34,
    category: item.category,
    data: wrapped.buffer,
    compressAlgo: item.compress_algo,
    originalSize: wrapped.originalSize,
  };
  return {
    name: item.name,
    category: item.category,
    input: item.data,
    compress_algo: item.compress_algo,
    payload_hex: hex(wrapped.buffer),
    original_size: wrapped.originalSize,
    debug_proto_hex: hex(encode(protobuf.WARemoteDebug_DebugMessage, proto)),
    unwrapped: codex.unwrapDebugMessageData(proto),
  };
});
const compressionAfterRoundTrips = codex.model.getCompressionSavedBytes();

const emptyPingPayload = encode(protobuf.WARemoteDebug_Ping, { pingId: 9, payload: "empty-category" });
const emptyCategoryProto = { seq: 3, after: 4, category: "", data: emptyPingPayload, compressAlgo: 0, originalSize: 0 };
const unknownProto = { seq: 5, after: 6, category: "futureCategory", data: Buffer.from([8, 150, 1]), compressAlgo: 0, originalSize: 0 };

const compressedPayload = codex.wrapDebugMessageData({ ping_id: 10, payload: "fallback-".repeat(30) }, constants.DebugMessageCategory.Ping, constants.CompressAlgo.Zlib);
const fallbackProto = {
  seq: 7,
  after: 8,
  category: constants.DebugMessageCategory.Ping,
  data: compressedPayload.buffer,
  compressAlgo: constants.CompressAlgo.Zlib,
  originalSize: 0,
};
const specialUnwrap = [
  { name: "empty_category_defaults_ping", debug_proto_hex: hex(encode(protobuf.WARemoteDebug_DebugMessage, emptyCategoryProto)), output: codex.unwrapDebugMessageData(emptyCategoryProto) },
  { name: "unknown_category_empty_object", debug_proto_hex: hex(encode(protobuf.WARemoteDebug_DebugMessage, unknownProto)), output: codex.unwrapDebugMessageData(unknownProto) },
  { name: "compressed_original_size_fallback", debug_proto_hex: hex(encode(protobuf.WARemoteDebug_DebugMessage, fallbackProto)), compressed_size: fallbackProto.data.length, output: codex.unwrapDebugMessageData(fallbackProto) },
];

const C = constants;
const commandTypes = ["Login", "Heartbeat", "JoinRoom", "QuitRoom", "SendDebugMessage", "SendDebugMessageParallelly", "MessageNotify", "MessageNotifyParallelly", "SyncMessage", "EventNotifyBegin", "EventNotifyBlock", "EventNotifyEnd"];
const commandMapping = commandTypes.map((name) => ({
  name,
  request_type: C.RequestType[name],
  request_cmd: codex.requestCmdForType(C.RequestType[name]),
  client_response_type: C.ClientResponseType[name],
  client_request_cmd: codex.clientRequestCmdForType(C.ClientResponseType[name]),
}));
commandMapping.push({ name: "UnknownNumeric", request_type: 4242, request_cmd: codex.requestCmdForType(4242), client_response_type: 4242, client_request_cmd: codex.clientRequestCmdForType(4242) });

const developerOutgoingInputs = [
  ["login", C.RequestType.Login, { base_request: { clientversion: 123 }, newticket: "ticket", autodev: 1 }],
  ["heartbeat", C.RequestType.Heartbeat, { base_request: { clientversion: 124 }, recv_ack: 19 }],
  ["join", C.RequestType.JoinRoom, { base_request: { clientversion: 125 }, appid: "wx-app", room_id: "room-1", wxpkg_info: "pkg" }],
  ["quit", C.RequestType.QuitRoom, { base_request: { clientversion: 126 } }],
  ["sync_with_max", C.RequestType.SyncMessage, { base_request: { clientversion: 127 }, min_seq: 4, max_seq: 99 }],
  ["sync_without_max", C.RequestType.SyncMessage, { base_request: { clientversion: 128 }, min_seq: 5 }],
];
const developerOutgoing = developerOutgoingInputs.map(([name, type, input], index) => {
  const uuid = `dev-${index}`;
  const result = caught(() => codex.wrapOutgoingToProto(input, type, uuid));
  return { name, type, uuid, input, frame_hex: result.value ? hex(result.value) : null, error: result.error };
});
const debugMessageInput = { seq: 1, after: 2, category: C.DebugMessageCategory.Ping, data: { ping_id: 2, payload: "x" }, compress_algo: 0 };
for (const [name, type, input, parallel] of [
  ["send_debug", C.RequestType.SendDebugMessage, { base_request: { clientversion: 129 }, recv_ack: 7, debug_message: [debugMessageInput] }, false],
  ["send_debug_parallel_request", C.RequestType.SendDebugMessageParallelly, { base_request: { clientversion: 130 }, recv_ack: 8, debug_message: [debugMessageInput] }, false],
  ["send_debug_parallel_notify", C.RequestType.SendDebugMessageParallelly, { debug_message: [debugMessageInput] }, true],
  ["message_notify_parallel_response", C.ResponseType.MessageNotifyParallelly, {}, false],
]) {
  const result = caught(() => codex.wrapOutgoingToProto(input, type, name, parallel));
  developerOutgoing.push({ name, type, uuid: name, input, parallel, frame_hex: result.value ? hex(result.value) : null, error: result.error });
}
developerOutgoing.push({ name: "unknown_type", type: 4242, uuid: "bad", input: {}, ...caught(() => codex.wrapOutgoingToProto({}, 4242, "bad")), frame_hex: null });
delete developerOutgoing[developerOutgoing.length - 1].value;

const clientResponses = [
  ["login", C.ClientResponseType.Login, { base_response: { errcode: -2, errmsg: "bad" }, room_info: { join_room: true, original_md5: "md5", room_status: 3, wx_conn_status: 4, dev_conn_status: 5, room_id: "room-x" } }],
  ["heartbeat", C.ClientResponseType.Heartbeat, { base_response: { errcode: 0, errmsg: "" } }],
  ["join", C.ClientResponseType.JoinRoom, { base_response: { errcode: 1, errmsg: "join" } }],
  ["quit", C.ClientResponseType.QuitRoom, { base_response: { errcode: 0, errmsg: "bye" } }],
  ["sync", C.ClientResponseType.SyncMessage, { base_response: { errcode: 0, errmsg: "" }, debug_message: [{ seq: 1, delay: 17, category: C.DebugMessageCategory.Ping, data: { ping_id: 2, payload: "sync" }, compress_algo: 0 }], send_ack: 88 }],
  ["notify_parallel", C.ClientResponseType.MessageNotifyParallelly, { debug_message: [{ seq: 2, delay: 18, category: C.DebugMessageCategory.ChromeDevtools, data: { op_id: 3, payload: "{}", jscontext_id: "ctx" }, compress_algo: 0 }] }],
  ["event_begin", C.ClientResponseType.EventNotifyBegin, {}],
  ["event_block", C.ClientResponseType.EventNotifyBlock, {}],
  ["event_end", C.ClientResponseType.EventNotifyEnd, {}],
];
const clientResponseFrames = clientResponses.map(([name, type, input], index) => {
  const uuid = `client-${index}`;
  const result = caught(() => codex.wrapClientResponseDataFormatToProto(input, type, uuid));
  return { name, type, uuid, input, frame_hex: result.value ? hex(result.value) : null, error: result.error };
});
{
  const result = caught(() => codex.wrapClientResponseDataFormatToProto({}, 4242, "client-bad"));
  clientResponseFrames.push({ name: "unknown_type", type: 4242, uuid: "client-bad", input: {}, frame_hex: null, error: result.error });
}

const incoming = [
  ["client_login", C.ClientRequestCmd.Login, protobuf.WARemoteDebug_WxLoginReq, { baseRequest: { clientVersion: 201 }, loginTicket: "login-ticket" }, "client"],
  ["client_heartbeat", C.ClientRequestCmd.Heartbeat, protobuf.WARemoteDebug_WxHeartBeatReq, { baseRequest: { clientVersion: 202 }, recvAck: 44 }, "client"],
  ["client_join", C.ClientRequestCmd.JoinRoom, protobuf.WARemoteDebug_WxJoinRoomReq, { baseRequest: { clientVersion: 203 }, username: "user", roomId: "room", wxpkgInfo: "pkg" }, "client"],
  ["client_quit", C.ClientRequestCmd.QuitRoom, protobuf.WARemoteDebug_WxQuitRoomReq, { baseRequest: { clientVersion: 204 } }, "client"],
  ["client_sync", C.ClientRequestCmd.SyncMessage, protobuf.WARemoteDebug_WxSyncMessageReq, { baseRequest: { clientVersion: 205 }, minSeq: 11, maxSeq: 22 }, "client"],
  ["developer_login_response", C.ResponseType.Login, protobuf.WARemoteDebug_DevLoginResp, { baseResponse: { errcode: 0, errmsg: "ok" }, roomInfo: { joinRoom: true, roomId: "r", originalMd5: "m", roomStatus: 1, wxConnStatus: 2, devConnStatus: 3 } }, "developer"],
  ["developer_heartbeat_response", C.ResponseType.Heartbeat, protobuf.WARemoteDebug_DevHeartBeatResp, { baseResponse: { errcode: -1, errmsg: "heartbeat" } }, "developer"],
  ["developer_join_response", C.ResponseType.JoinRoom, protobuf.WARemoteDebug_DevJoinRoomResp, { baseResponse: { errcode: 0, errmsg: "joined" } }, "developer"],
  ["developer_quit_response", C.ResponseType.QuitRoom, protobuf.WARemoteDebug_DevQuitRoomResp, { baseResponse: { errcode: 0, errmsg: "quit" } }, "developer"],
  ["developer_sync_response", C.ResponseType.SyncMessage, protobuf.WARemoteDebug_DevSyncMessageResp, { baseResponse: { errcode: 0, errmsg: "sync" }, debugMessageList: [emptyCategoryProto], sendAck: 66 }, "developer"],
  ["client_send_debug_default", C.ClientRequestCmd.SendDebugMessage, protobuf.WARemoteDebug_SendDebugMessageReq, { baseRequest: { clientVersion: 206 } }, "client"],
  ["client_send_debug_parallel", C.ClientRequestCmd.SendDebugMessageParallelly, protobuf.WARemoteDebug_NewSendDebugMessageReq, { baseRequest: { clientVersion: 207 }, recvAck: 1 }, "client"],
  ["client_message_notify_default", C.ClientRequestCmd.MessageNotify, protobuf.WARemoteDebug_MessageNotify, {}, "client"],
  ["client_message_notify_parallel_default", C.ClientRequestCmd.MessageNotifyParallelly, protobuf.WARemoteDebug_MessageNotify, {}, "client"],
  ["client_event_begin_default", C.ClientRequestCmd.EventNotifyBegin, protobuf.WARemoteDebug_EventNotify, {}, "client"],
  ["client_event_block_default", C.ClientRequestCmd.EventNotifyBlock, protobuf.WARemoteDebug_EventNotify, {}, "client"],
  ["client_event_end_default", C.ClientRequestCmd.EventNotifyEnd, protobuf.WARemoteDebug_EventNotify, {}, "client"],
  ["developer_send_debug_response", C.ResponseType.SendDebugMessage, protobuf.WARemoteDebug_SendDebugMessageResp, { baseResponse: { errcode: 0, errmsg: "send" }, sendAck: 2 }, "developer"],
  ["developer_send_debug_parallel_response", C.ResponseType.SendDebugMessageParallelly, protobuf.WARemoteDebug_NewSendDebugMessageResp, { baseResponse: { errcode: 0, errmsg: "parallel" }, minAck: 1, maxAck: 2 }, "developer"],
  ["developer_message_notify", C.ResponseType.MessageNotify, protobuf.WARemoteDebug_MessageNotify, {}, "developer"],
  ["developer_message_notify_parallel", C.ResponseType.MessageNotifyParallelly, protobuf.WARemoteDebug_MessageNotify, {}, "developer"],
  ["developer_event_begin", C.ResponseType.EventNotifyBegin, protobuf.WARemoteDebug_EventNotify, {}, "developer"],
  ["developer_event_block", C.ResponseType.EventNotifyBlock, protobuf.WARemoteDebug_EventNotify, {}, "developer"],
  ["developer_event_end", C.ResponseType.EventNotifyEnd, protobuf.WARemoteDebug_EventNotify, {}, "developer"],
];
const incomingFrames = incoming.map(([name, cmd, Type, value, direction], index) => {
  const frame = dataFormat(cmd, `incoming-${index}`, Type, value);
  const output = direction === "client" ? codex.unwrapClientProtoToDataFormat(frame) : codex.unwrapProtoToDataFormat(frame);
  return { name, direction, cmd, frame_hex: hex(frame), output };
});
const unknownFrame = encode(protobuf.WARemoteDebug_DataFormat, { cmd: 4242, uuid: "unknown", data: Buffer.alloc(0) });
incomingFrames.push({ name: "unknown_client_cmd", direction: "client", cmd: 4242, frame_hex: hex(unknownFrame), output: codex.unwrapClientProtoToDataFormat(unknownFrame) });
incomingFrames.push({ name: "unknown_developer_cmd", direction: "developer", cmd: 4242, frame_hex: hex(unknownFrame), output: codex.unwrapProtoToDataFormat(unknownFrame) });

const fixture = {
  reference_commit: "2b90b77fc6f13dd18480cd07d7dd9c052cc26c9d",
  debug_wrap: debugWrap,
  category_branches: categoryBranches,
  special_unwrap: specialUnwrap,
  compression_saved_bytes_after_round_trips: compressionAfterRoundTrips,
  command_mapping: commandMapping,
  developer_outgoing: developerOutgoing,
  client_response_frames: clientResponseFrames,
  incoming_frames: incomingFrames,
};

const output = path.join(root, "testdata/golden/reference_codex.json");
fs.writeFileSync(output, JSON.stringify(fixture, null, 2) + "\n");
console.log(`${output}: ${debugWrap.length} debug, ${developerOutgoing.length} developer, ${clientResponseFrames.length} client responses, ${incomingFrames.length} incoming`);
