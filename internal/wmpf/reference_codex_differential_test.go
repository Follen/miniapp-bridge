package wmpf

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

type referenceCodexFixture struct {
	ReferenceCommit string `json:"reference_commit"`
	DebugWrap       []struct {
		Name          string          `json:"name"`
		Category      string          `json:"category"`
		Input         json.RawMessage `json:"input"`
		CompressAlgo  uint32          `json:"compress_algo"`
		PayloadHex    string          `json:"payload_hex"`
		OriginalSize  uint32          `json:"original_size"`
		DebugProtoHex string          `json:"debug_proto_hex"`
	} `json:"debug_wrap"`
	CategoryBranches []struct {
		Name           string  `json:"name"`
		Category       string  `json:"category"`
		PayloadHex     string  `json:"payload_hex"`
		WrapPayloadHex *string `json:"wrap_payload_hex"`
		WrapError      *string `json:"wrap_error"`
	} `json:"category_branches"`
	SpecialUnwrap []struct {
		Name           string `json:"name"`
		DebugProtoHex  string `json:"debug_proto_hex"`
		CompressedSize uint32 `json:"compressed_size"`
		Output         struct {
			Category     string          `json:"category"`
			Data         json.RawMessage `json:"data"`
			OriginalSize uint32          `json:"original_size"`
		} `json:"output"`
	} `json:"special_unwrap"`
	CommandMapping []struct {
		Name             string `json:"name"`
		RequestType      int64  `json:"request_type"`
		RequestCmd       int64  `json:"request_cmd"`
		ClientResponse   int64  `json:"client_response_type"`
		ClientRequestCmd int64  `json:"client_request_cmd"`
	} `json:"command_mapping"`
	DeveloperOutgoing     []referenceFrame `json:"developer_outgoing"`
	ClientResponses       []referenceFrame `json:"client_response_frames"`
	CompressionSavedBytes uint32           `json:"compression_saved_bytes_after_round_trips"`
	IncomingFrames        []struct {
		Name      string `json:"name"`
		Direction string `json:"direction"`
		Cmd       uint32 `json:"cmd"`
		FrameHex  string `json:"frame_hex"`
	} `json:"incoming_frames"`
}

func TestReferenceCodexAllCategoryBranches(t *testing.T) {
	fixture := loadReferenceCodex(t)
	wrapValues := map[string]any{
		"call":            CallInterface{ObjName: "wx", MethodName: "m", MethodArgs: []string{"a"}, CallID: 1},
		"evaluate_result": EvaluateJavascriptResult{Ret: "1", EvaluateID: 3},
		"breakpoint":      Breakpoint{},
		"ping":            Ping{PingID: 4, Payload: "p"},
		"dom_op":          DomOp{Params: "{}", WebviewId: 6},
		"dom_event":       DomEvent{Params: "{}", WebviewId: 7},
		"chrome":          ChromeDevtools{OpID: 9, Payload: "{}", JSContextID: "c"},
		"connect_context": JsContext{ID: "c"},
		"custom":          CustomMessage{Method: "m", Payload: "{}", Raw: []byte{1, 2}},
	}
	if len(fixture.CategoryBranches) != 19 {
		t.Fatalf("category branch count=%d, want 19", len(fixture.CategoryBranches))
	}
	wrapOK, wrapErrors := 0, 0
	for _, tc := range fixture.CategoryBranches {
		t.Run(tc.Name, func(t *testing.T) {
			decoded, err := DecodeCategory(tc.Category, mustHex(t, tc.PayloadHex))
			if err != nil {
				t.Fatalf("unwrap category: %v", err)
			}
			if tc.Name == "engine_event" || tc.Name == "engine_op" {
				if !reflect.DeepEqual(decoded, map[string]any{}) {
					t.Fatalf("default category decoded as %#v", decoded)
				}
			}
			if tc.WrapError != nil {
				wrapErrors++
				if *tc.WrapError == "" {
					t.Fatal("reference error branch has empty error")
				}
				return
			}
			wrapOK++
			value, ok := wrapValues[tc.Name]
			if !ok {
				t.Fatalf("missing Go value for successful reference wrap branch")
			}
			encoded, err := EncodeCategory(tc.Category, value)
			if err != nil {
				t.Fatal(err)
			}
			if got := hex.EncodeToString(encoded); tc.WrapPayloadHex == nil || got != *tc.WrapPayloadHex {
				t.Fatalf("wrap payload=%s, fixed reference=%v", got, tc.WrapPayloadHex)
			}
		})
	}
	if wrapOK != 9 || wrapErrors != 10 {
		t.Fatalf("wrap branches ok=%d errors=%d, want 9/10", wrapOK, wrapErrors)
	}
}

type referenceFrame struct {
	Name     string `json:"name"`
	Type     uint32 `json:"type"`
	UUID     string `json:"uuid"`
	FrameHex string `json:"frame_hex"`
	Error    string `json:"error"`
	Parallel bool   `json:"parallel"`
}

func loadReferenceCodex(t *testing.T) referenceCodexFixture {
	t.Helper()
	p := filepath.Join("..", "..", "testdata", "golden", "reference_codex.json")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var fixture referenceCodexFixture
	if err := json.Unmarshal(b, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.ReferenceCommit != "2b90b77fc6f13dd18480cd07d7dd9c052cc26c9d" {
		t.Fatalf("unexpected reference commit %q", fixture.ReferenceCommit)
	}
	return fixture
}

func mustHex(t *testing.T, value string) []byte {
	t.Helper()
	b, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mustEncodeCategory(t *testing.T, category string, value any) []byte {
	t.Helper()
	b, err := EncodeCategory(category, value)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestReferenceCodexDebugObjectAndCompression(t *testing.T) {
	fixture := loadReferenceCodex(t)
	values := map[string]any{
		"ping_snake":                      Ping{PingID: 900719925, Payload: "ping-payload"},
		"ping_zlib":                       Ping{PingID: 77, Payload: "compress-me-compress-me-compress-me-compress-me-compress-me-compress-me-compress-me-compress-me-compress-me-compress-me-compress-me-compress-me-compress-me-compress-me-compress-me-compress-me-compress-me-compress-me-compress-me-compress-me-compress-me-compress-me-compress-me-compress-me-"},
		"ping_zlib_bitmask":               Ping{PingID: 78, Payload: "bitmask-bitmask-bitmask-bitmask-bitmask-bitmask-bitmask-bitmask-bitmask-bitmask-bitmask-bitmask-bitmask-bitmask-bitmask-bitmask-bitmask-bitmask-bitmask-bitmask-bitmask-bitmask-bitmask-bitmask-bitmask-bitmask-bitmask-bitmask-bitmask-bitmask-bitmask-bitmask-"},
		"call_interface_stringifies_args": CallInterface{ObjName: "wx", MethodName: "invoke", MethodArgs: []string{"7", "true", "null", "raw"}, CallID: 41},
		"breakpoint_boolean":              Breakpoint{IsHit: true},
		"chrome_snake":                    ChromeDevtools{OpID: 55, Payload: `{"id":55}`, JSContextID: "ctx-main"},
		"custom_raw":                      CustomMessage{Method: "notice", Payload: "{}", Raw: []byte{0, 1, 254, 255}},
	}
	for _, tc := range fixture.DebugWrap {
		t.Run(tc.Name, func(t *testing.T) {
			payload, err := EncodeCategory(tc.Category, values[tc.Name])
			if err != nil {
				t.Fatal(err)
			}
			wrapped, original, err := WrapData(payload, tc.Category, CompressAlgo(tc.CompressAlgo))
			if err != nil {
				t.Fatal(err)
			}
			if got := hex.EncodeToString(wrapped); got != tc.PayloadHex {
				t.Errorf("payload differs\n got %s\nwant %s", got, tc.PayloadHex)
			}
			if original != tc.OriginalSize {
				t.Errorf("original size = %d, want %d", original, tc.OriginalSize)
			}
			proto := EncodeDebugMessage(DebugMessage{Seq: 12, After: 34, Category: tc.Category, Data: wrapped, CompressAlgo: CompressAlgo(tc.CompressAlgo), OriginalSize: original})
			if got := hex.EncodeToString(proto); got != tc.DebugProtoHex {
				t.Errorf("debug protobuf differs\n got %s\nwant %s", got, tc.DebugProtoHex)
			}
		})
	}
}

func TestReferenceCodexCompressionStatistics(t *testing.T) {
	fixture := loadReferenceCodex(t)
	ResetCompressionStatistics()
	var expected uint32
	for _, tc := range fixture.DebugWrap {
		if tc.CompressAlgo&uint32(CompressZlib) == 0 {
			continue
		}
		fixtureCompressed := mustHex(t, tc.PayloadHex)
		raw, err := zlibDecompress(fixtureCompressed)
		if err != nil {
			t.Fatal(err)
		}
		compressedData, originalSize, err := WrapData(raw, tc.Category, CompressZlib)
		if err != nil {
			t.Fatal(err)
		}
		if originalSize != tc.OriginalSize {
			t.Fatalf("original size = %d, reference %d", originalSize, tc.OriginalSize)
		}
		if _, err := UnwrapDebugMessage(DebugMessage{Category: tc.Category, Data: compressedData, CompressAlgo: CompressZlib, OriginalSize: originalSize}); err != nil {
			t.Fatal(err)
		}
		compressed := uint32(len(fixtureCompressed))
		expected += 2 * (tc.OriginalSize - compressed)
	}
	if fixture.CompressionSavedBytes != expected {
		t.Fatalf("reference compression statistic = %d, calculated %d", fixture.CompressionSavedBytes, expected)
	}
	if got := CompressionSavedBytes(); got != int64(expected) {
		t.Fatalf("compression statistic = %d, reference %d", got, expected)
	}
	if runtime.GOOS == "windows" && ZlibVersion() != "1.3.1" {
		t.Fatalf("zlib version = %q, want 1.3.1", ZlibVersion())
	}
}

func TestReferenceCodexSpecialUnwrapSemantics(t *testing.T) {
	fixture := loadReferenceCodex(t)
	for _, tc := range fixture.SpecialUnwrap {
		t.Run(tc.Name, func(t *testing.T) {
			var proto DebugMessageProto
			if err := UnmarshalMessage(mustHex(t, tc.DebugProtoHex), &proto); err != nil {
				t.Fatal(err)
			}
			unwrapped, err := UnwrapDebugMessage(DebugMessage{Seq: proto.Seq, After: proto.After, Category: proto.Category, Data: proto.Data, CompressAlgo: CompressAlgo(proto.CompressAlgo), OriginalSize: proto.OriginalSize})
			if err != nil {
				t.Fatal(err)
			}
			if unwrapped.Category != tc.Output.Category {
				t.Errorf("category = %q, reference %q", unwrapped.Category, tc.Output.Category)
			}
			if unwrapped.OriginalSize != tc.Output.OriginalSize {
				t.Errorf("original size = %d, reference %d", unwrapped.OriginalSize, tc.Output.OriginalSize)
			}
			raw, ok := unwrapped.Data.([]byte)
			if !ok {
				t.Fatalf("unwrapped data type = %T, want []byte", unwrapped.Data)
			}
			decoded, err := DecodeCategory(unwrapped.Category, raw)
			if err != nil {
				t.Fatal(err)
			}
			if tc.Name == "unknown_category_empty_object" && !reflect.DeepEqual(decoded, map[string]any{}) {
				t.Errorf("unknown category data = %#v, reference empty object", decoded)
			}
		})
	}
}

func TestReferenceCodexCommandMapping(t *testing.T) {
	fixture := loadReferenceCodex(t)
	for _, tc := range fixture.CommandMapping {
		t.Run(tc.Name, func(t *testing.T) {
			if tc.Name == "UnknownNumeric" {
				if tc.RequestCmd != -1 || tc.ClientRequestCmd != -1 {
					t.Fatalf("reference unknown mapping changed: %#v", tc)
				}
				return
			}
			if tc.RequestType != tc.RequestCmd {
				t.Errorf("developer mapping %d -> %d", tc.RequestType, tc.RequestCmd)
			}
			if tc.ClientResponse != tc.ClientRequestCmd {
				t.Errorf("client mapping %d -> %d", tc.ClientResponse, tc.ClientRequestCmd)
			}
		})
	}
}

func TestReferenceCodexDeveloperOutgoingFrames(t *testing.T) {
	fixture := loadReferenceCodex(t)
	if len(fixture.DeveloperOutgoing) != 11 {
		t.Fatalf("developer outgoing branch count=%d, want 11", len(fixture.DeveloperOutgoing))
	}
	values := map[string]any{
		"login":                       DevLoginReq{BaseRequest: &BaseReq{ClientVersion: 123}, Newticket: "ticket", Autodev: 1},
		"heartbeat":                   DevHeartBeatReq{BaseRequest: &BaseReq{ClientVersion: 124}, RecvAck: 19},
		"join":                        DevJoinRoomReq{BaseRequest: &BaseReq{ClientVersion: 125}, Appid: "wx-app", RoomId: "room-1", WxpkgInfo: "pkg"},
		"quit":                        DevQuitRoomReq{BaseRequest: &BaseReq{ClientVersion: 126}},
		"sync_with_max":               DevSyncMessageReq{BaseRequest: &BaseReq{ClientVersion: 127}, MinSeq: 4, MaxSeq: 99},
		"sync_without_max":            DevSyncMessageReq{BaseRequest: &BaseReq{ClientVersion: 128}, MinSeq: 5},
		"send_debug":                  SendDebugMessageReq{BaseRequest: &BaseReq{ClientVersion: 129}, RecvAck: 7, DebugMessageList: []DebugMessageProto{{Seq: 1, After: 2, Category: CategoryPing, Data: mustEncodeCategory(t, CategoryPing, Ping{PingID: 2, Payload: "x"})}}},
		"send_debug_parallel_request": NewSendDebugMessageReq{BaseRequest: &BaseReq{ClientVersion: 130}, RecvAck: 8, DebugMessageList: []DebugMessageProto{{Seq: 1, After: 2, Category: CategoryPing, Data: mustEncodeCategory(t, CategoryPing, Ping{PingID: 2, Payload: "x"})}}},
		"send_debug_parallel_notify":  MessageNotify{DebugMessageList: []DebugMessageProto{{Seq: 1, After: 2, Category: CategoryPing, Data: mustEncodeCategory(t, CategoryPing, Ping{PingID: 2, Payload: "x"})}}},
	}
	for i, tc := range fixture.DeveloperOutgoing {
		t.Run(tc.Name, func(t *testing.T) {
			if tc.Name == "unknown_type" {
				if _, err := EncodeCommandPayload(RoleDeveloper, tc.Type, struct{}{}); err == nil {
					t.Fatal("unknown command accepted")
				}
				return
			}
			if tc.Name == "message_notify_parallel_response" {
				payload := []byte{0x0a, 0x04, 0x08, 0x00, 0x12, 0x00}
				frame := encodeDataFormatCodex(tc.Type, tc.UUID, payload)
				if got := hex.EncodeToString(frame); got != tc.FrameHex {
					t.Errorf("frame differs\n got %s\nwant %s", got, tc.FrameHex)
				}
				return
			}
			if tc.Name == "send_debug" || tc.Name == "send_debug_parallel_request" || tc.Name == "send_debug_parallel_notify" {
				value := values[tc.Name]
				frame, err := EncodeDeveloperOutgoingDataFormat(tc.Type, tc.UUID, value, tc.Name == "send_debug_parallel_notify")
				if err != nil {
					t.Fatal(err)
				}
				if got := hex.EncodeToString(frame); got != tc.FrameHex {
					t.Errorf("frame differs\n got %s\nwant %s", got, tc.FrameHex)
				}
				return
			}
			command := tc.Type
			if tc.Name == "send_debug_parallel_notify" {
				command = CmdDevMessageNotifyParallel
			}
			payload, err := EncodeCommandPayload(RoleDeveloper, command, values[tc.Name])
			if err != nil {
				t.Fatal(err)
			}
			uuid := tc.UUID
			if uuid == "" {
				uuid = "dev-" + string(rune('0'+i))
			}
			frame, err := EncodeDataFormat(DataFormat{Cmd: tc.Type, Uuid: uuid, Data: payload})
			if err != nil {
				t.Fatal(err)
			}
			if got := hex.EncodeToString(frame); got != tc.FrameHex {
				t.Errorf("frame differs\n got %s\nwant %s", got, tc.FrameHex)
			}
		})
	}
}

func TestReferenceCodexClientResponseFrames(t *testing.T) {
	fixture := loadReferenceCodex(t)
	if len(fixture.ClientResponses) != 10 {
		t.Fatalf("client response branch count=%d, want 10", len(fixture.ClientResponses))
	}
	pingData, _ := EncodeCategory(CategoryPing, Ping{PingID: 2, Payload: "sync"})
	chromeData, _ := EncodeCategory(CategoryChromeDevtools, ChromeDevtools{OpID: 3, Payload: "{}", JSContextID: "ctx"})
	values := map[string]any{
		"login":           WxLoginResp{BaseResponse: &BaseResp{Errcode: -2, Errmsg: "bad"}, RoomInfo: &RoomInfo{JoinRoom: true, OriginalMd5: "md5", RoomStatus: 3, WxConnStatus: 4, DevConnStatus: 5, RoomId: "room-x"}},
		"heartbeat":       WxHeartBeatResp{BaseResponse: &BaseResp{}},
		"join":            WxJoinRoomResp{BaseResponse: &BaseResp{Errcode: 1, Errmsg: "join"}},
		"quit":            WxQuitRoomResp{BaseResponse: &BaseResp{Errmsg: "bye"}},
		"sync":            WxSyncMessageResp{BaseResponse: &BaseResp{}, DebugMessageList: []DebugMessageProto{{Seq: 1, Category: CategoryPing, Data: pingData}}, SendAck: 88},
		"notify_parallel": MessageNotify{DebugMessageList: []DebugMessageProto{{Seq: 2, Category: CategoryChromeDevtools, Data: chromeData}}},
		"event_begin":     EventNotify{},
		"event_block":     EventNotify{},
		"event_end":       EventNotify{},
	}
	for i, tc := range fixture.ClientResponses {
		t.Run(tc.Name, func(t *testing.T) {
			if tc.Name == "unknown_type" {
				if _, err := EncodeClientResponseDataFormat(tc.Type, tc.UUID, struct{}{}); err == nil {
					t.Fatal("unknown client response command accepted")
				}
				return
			}
			uuid := tc.UUID
			if uuid == "" {
				uuid = "client-" + string(rune('0'+i))
			}
			frame, err := EncodeClientResponseDataFormat(tc.Type, uuid, values[tc.Name])
			if err != nil {
				t.Fatal(err)
			}
			if got := hex.EncodeToString(frame); got != tc.FrameHex {
				t.Errorf("frame differs\n got %s\nwant %s", got, tc.FrameHex)
			}
		})
	}
}

func TestReferenceCodexIncomingDirectionTypes(t *testing.T) {
	fixture := loadReferenceCodex(t)
	expected := map[string]reflect.Type{
		"client_login":                           reflect.TypeOf(&WxLoginReq{}),
		"client_heartbeat":                       reflect.TypeOf(&WxHeartBeatReq{}),
		"client_join":                            reflect.TypeOf(&WxJoinRoomReq{}),
		"client_quit":                            reflect.TypeOf(&WxQuitRoomReq{}),
		"client_sync":                            reflect.TypeOf(&WxSyncMessageReq{}),
		"client_send_debug_parallel":             reflect.TypeOf(&NewSendDebugMessageReq{}),
		"developer_login_response":               reflect.TypeOf(&DevLoginResp{}),
		"developer_heartbeat_response":           reflect.TypeOf(&DevHeartBeatResp{}),
		"developer_join_response":                reflect.TypeOf(&DevJoinRoomResp{}),
		"developer_quit_response":                reflect.TypeOf(&DevQuitRoomResp{}),
		"developer_sync_response":                reflect.TypeOf(&DevSyncMessageResp{}),
		"developer_send_debug_response":          reflect.TypeOf(&SendDebugMessageResp{}),
		"developer_send_debug_parallel_response": reflect.TypeOf(&NewSendDebugMessageResp{}),
		"developer_message_notify":               reflect.TypeOf(&MessageNotify{}),
		"developer_message_notify_parallel":      reflect.TypeOf(&MessageNotify{}),
		"developer_event_begin":                  reflect.TypeOf(&EventNotify{}),
		"developer_event_block":                  reflect.TypeOf(&EventNotify{}),
		"developer_event_end":                    reflect.TypeOf(&EventNotify{}),
	}
	for _, tc := range fixture.IncomingFrames {
		t.Run(tc.Name, func(t *testing.T) {
			frame, err := DecodeDataFormat(mustHex(t, tc.FrameHex))
			if err != nil {
				t.Fatal(err)
			}
			role := RoleDeveloper
			if tc.Direction == "client" {
				role = RoleClient
			}
			value, err := DecodeCommandPayload(role, frame.Cmd, frame.Data)
			wantType, typed := expected[tc.Name]
			if !typed {
				if err != nil {
					t.Errorf("reference returns empty object for unknown command: %v", err)
				}
				if !reflect.DeepEqual(value, map[string]any{}) {
					t.Errorf("reference default returns empty object, got %#v", value)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := reflect.TypeOf(value); got != wantType {
				t.Errorf("decoded type = %v, reference direction requires %v", got, wantType)
			}
		})
	}
}
