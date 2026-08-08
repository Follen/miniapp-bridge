package wmpf

import (
	"errors"
	"reflect"
	"testing"
)

func requireError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCoverageGenericCodecBranches(t *testing.T) {
	if HasMessageType("missing") {
		t.Fatal("unknown type reported present")
	}
	if _, err := DecodeGeneric("missing", nil); err == nil {
		t.Fatal("unknown type decoded")
	}
	cases := map[string][]byte{
		"tag varint":       {0x80},
		"field zero":       {0x00, 0x00},
		"value varint":     {0x08, 0x80},
		"fixed64 short":    {0x09, 1},
		"length varint":    {0x12, 0x80},
		"length body":      {0x12, 2, 1},
		"fixed32 short":    {0x0d, 1},
		"unsupported wire": {0x0b},
	}
	for name, wire := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := DecodeGeneric("WARemoteDebug_Ping", wire)
			requireError(t, err)
		})
	}
	valid := []byte{0x08, 1, 0x11, 1, 2, 3, 4, 5, 6, 7, 8, 0x1a, 1, 'x', 0x25, 1, 2, 3, 4}
	m, err := DecodeGeneric("WARemoteDebug_Ping", valid)
	if err != nil || len(m.Fields) != 4 {
		t.Fatalf("valid all-wire message: fields=%d err=%v", len(m.Fields), err)
	}
}

type coveragePointerList struct {
	ProtoUnknown
	Items []*BaseReq `pb:"1,msg"`
}

func TestCoverageReflectionCodecErrors(t *testing.T) {
	var nilPing *PingProto
	if b, err := MarshalMessage(nilPing); err != nil || b != nil {
		t.Fatalf("nil pointer marshal=%x err=%v", b, err)
	}
	_, err := MarshalMessage(1)
	requireError(t, err)
	requireError(t, UnmarshalMessage(nil, nil))
	var nilOut *PingProto
	requireError(t, UnmarshalMessage(nil, nilOut))
	requireError(t, UnmarshalMessage(nil, new(int)))

	var ping PingProto
	requireError(t, UnmarshalMessage([]byte{0x80}, &ping))
	var device DeviceInfo
	requireError(t, UnmarshalMessage([]byte{0x35, 1, 2}, &device))

	var pointers coveragePointerList
	if err := UnmarshalMessage([]byte{0x0a, 0x02, 0x08, 1}, &pointers); err != nil || len(pointers.Items) != 1 {
		t.Fatalf("pointer repeated decode=%+v err=%v", pointers, err)
	}
	requireError(t, UnmarshalMessage([]byte{0x0a, 1, 0x80}, &pointers))
	var values MessageNotify
	requireError(t, UnmarshalMessage([]byte{0x0a, 1, 0x80}, &values))
	var singular CommReq
	requireError(t, UnmarshalMessage([]byte{0x0a, 1, 0x80}, &singular))
}

func TestCoverageSkipAllWireBranches(t *testing.T) {
	tests := []struct {
		name string
		wire int
		data []byte
		ok   bool
	}{
		{"varint", 0, []byte{1}, true},
		{"fixed64", 1, make([]byte, 8), true},
		{"fixed64 short", 1, nil, false},
		{"bytes", 2, []byte{1, 'x'}, true},
		{"group end", 3, []byte{0x0c}, true},
		{"group eof", 3, nil, false},
		{"group tag error", 3, []byte{0x80}, false},
		{"group child error", 3, []byte{0x0f}, false},
		{"end group", 4, nil, true},
		{"fixed32", 5, make([]byte, 4), true},
		{"fixed32 short", 5, nil, false},
		{"unsupported", 7, nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			i := 0
			err := skip(tc.data, &i, tc.wire)
			if (err == nil) != tc.ok {
				t.Fatalf("err=%v want ok=%v", err, tc.ok)
			}
		})
	}
}

func TestCoverageSpecializedDecodersErrors(t *testing.T) {
	debugCases := [][]byte{
		{0x80}, {0x08}, {0x10}, {0x1a}, {0x22}, {0x28}, {0x30}, {0x3f},
	}
	for _, wire := range debugCases {
		_, err := DecodeDebugMessage(wire)
		requireError(t, err)
	}
	chromeCases := [][]byte{{0x80}, {0x08}, {0x12}, {0x1a}, {0x27}}
	for _, wire := range chromeCases {
		_, err := DecodeChrome(wire)
		requireError(t, err)
	}
	customCases := [][]byte{{0x80}, {0x0a}, {0x12}, {0x1a}, {0x27}}
	for _, wire := range customCases {
		_, err := DecodeCustom(wire)
		requireError(t, err)
	}
}

func TestCoverageCategoryErrors(t *testing.T) {
	decodeCategories := []string{
		CategoryPing, CategorySetupContext, CategoryCallInterface, CategoryCallInterfaceResult,
		CategoryEvaluateJavascript, CategoryEvaluateJavascriptResult, CategoryBreakpoint,
		CategoryPong, CategoryDomOp, CategoryDomEvent, CategoryNetworkDebugAPI,
		CategoryChromeDevtools, CategoryChromeDevtoolsResult, CategoryAddJsContext,
		CategoryRemoveJsContext, CategoryConnectJsContext, CategoryCustomMessage,
	}
	for _, category := range decodeCategories {
		t.Run("decode/"+category, func(t *testing.T) {
			_, err := DecodeCategory(category, []byte{0x80})
			requireError(t, err)
		})
	}
	badTypeCategories := []string{
		CategoryPing, CategoryCallInterface, CategoryCallInterfaceResult,
		CategoryEvaluateJavascript, CategoryEvaluateJavascriptResult, CategoryBreakpoint,
		CategoryPong, CategoryChromeDevtools, CategoryChromeDevtoolsResult,
		CategoryAddJsContext, CategoryRemoveJsContext, CategoryConnectJsContext, CategoryCustomMessage,
	}
	for _, category := range badTypeCategories {
		t.Run("encode/"+category, func(t *testing.T) {
			_, err := EncodeCategory(category, struct{}{})
			requireError(t, err)
		})
	}
	for _, category := range []string{CategorySetupContext, CategoryDomOp, CategoryDomEvent, CategoryNetworkDebugAPI} {
		_, err := EncodeCategory(category, 1)
		requireError(t, err)
	}
	if got, err := EncodeCategory("future", []byte{1, 2}); err != nil || !reflect.DeepEqual(got, []byte{1, 2}) {
		t.Fatalf("unknown raw=%x err=%v", got, err)
	}
	_, err := EncodeCategory("future", "bad")
	requireError(t, err)
}

func TestCoverageRegistryAndTransportErrors(t *testing.T) {
	if value, ok := NewMessage("missing"); ok || value != nil {
		t.Fatalf("unknown registry value=%v ok=%v", value, ok)
	}
	bad := []byte{0x80}
	if value, err := decodePayload(bad, &PingProto{}); err == nil || value != nil {
		t.Fatalf("decodePayload value=%v err=%v", value, err)
	}
	if _, err := DecodeResponsePayload(RoleClient, 9999, nil); err == nil {
		t.Fatal("unknown client response accepted")
	}
	if _, err := DecodeResponsePayload(RoleClient, CmdClientMessageNotify, nil); err != nil {
		t.Fatal(err)
	}
	if encodeBaseResponseCodex(nil) != nil {
		t.Fatal("nil base response encoded")
	}
	if got := encodeRoomInfoCodex(nil); len(got) != 0 {
		t.Fatalf("nil room info=%x", got)
	}

	wrongOutgoing := []struct {
		cmd      uint32
		parallel bool
	}{
		{CmdDevSendDebug, false},
		{CmdDevSendDebugParallel, false},
		{CmdDevSendDebugParallel, true},
		{9999, false},
	}
	for _, tc := range wrongOutgoing {
		_, err := EncodeDeveloperOutgoingDataFormat(tc.cmd, "u", struct{}{}, tc.parallel)
		requireError(t, err)
	}
	if _, err := EncodeDeveloperOutgoingDataFormat(CmdDevSendDebug, "u", SendDebugMessageReq{}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := EncodeDeveloperOutgoingDataFormat(CmdDevSendDebugParallel, "u", NewSendDebugMessageReq{}, false); err != nil {
		t.Fatal(err)
	}
	originalMarshal := marshalOutgoingMessage
	marshalOutgoingMessage = func(any) ([]byte, error) { return nil, errors.New("injected marshal failure") }
	defer func() { marshalOutgoingMessage = originalMarshal }()
	_, err := EncodeDeveloperOutgoingDataFormat(CmdDevSendDebug, "u", SendDebugMessageReq{BaseRequest: &BaseReq{}}, false)
	requireError(t, err)
	_, err = EncodeDeveloperOutgoingDataFormat(CmdDevSendDebugParallel, "u", NewSendDebugMessageReq{BaseRequest: &BaseReq{}}, false)
	requireError(t, err)
	marshalOutgoingMessage = originalMarshal

	wrongResponses := []uint32{
		CmdClientLogin, CmdClientHeartbeat, CmdClientJoinRoom, CmdClientQuitRoom,
		CmdClientSyncMessage, CmdClientSendDebugParallel, CmdClientMessageNotifyParallel,
		CmdEventBegin,
	}
	for _, cmd := range wrongResponses {
		_, err := EncodeClientResponseDataFormat(cmd, "u", struct{}{})
		requireError(t, err)
	}
	validNilResponses := []struct {
		cmd   uint32
		value any
	}{
		{CmdClientHeartbeat, WxHeartBeatResp{}},
		{CmdClientJoinRoom, WxJoinRoomResp{}},
		{CmdClientQuitRoom, WxQuitRoomResp{}},
		{CmdClientSyncMessage, WxSyncMessageResp{}},
		{CmdClientSendDebugParallel, NewSendDebugMessageResp{}},
	}
	for _, tc := range validNilResponses {
		if _, err := EncodeClientResponseDataFormat(tc.cmd, "u", tc.value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := EncodeClientResponseDataFormat(CmdClientSendDebugParallel, "u", NewSendDebugMessageResp{BaseResponse: &BaseResp{}}); err != nil {
		t.Fatal(err)
	}
}
