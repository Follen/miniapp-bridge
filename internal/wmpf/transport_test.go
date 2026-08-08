package wmpf

import (
	"reflect"
	"testing"
)

func TestDataFormatAndCommandRoundTrip(t *testing.T) {
	payload, err := EncodeCommandPayload(RoleDeveloper, CmdDevSendDebug, &SendDebugMessageReq{BaseRequest: &BaseReq{ClientVersion: 1}, DebugMessageList: []DebugMessageProto{{Seq: 9, Category: CategoryPing}}})
	if err != nil {
		t.Fatal(err)
	}
	frame, err := EncodeDataFormat(DataFormat{Cmd: CmdDevSendDebug, Uuid: "u", Data: payload})
	if err != nil {
		t.Fatal(err)
	}
	out, err := DecodeDataFormat(frame)
	if err != nil || out.Cmd != CmdDevSendDebug || out.Uuid != "u" {
		t.Fatalf("format=%+v err=%v", out, err)
	}
	decoded, err := DecodeRequestPayload(RoleDeveloper, out.Cmd, out.Data)
	if err != nil {
		t.Fatal(err)
	}
	req := decoded.(*SendDebugMessageReq)
	if len(req.DebugMessageList) != 1 || req.DebugMessageList[0].Seq != 9 {
		t.Fatalf("payload=%+v", req)
	}
}

func TestDecodeRequestPayloadByEndpoint(t *testing.T) {
	tests := []struct {
		name string
		role EndpointRole
		cmd  uint32
		want any
	}{
		{"client heartbeat", RoleClient, CmdClientHeartbeat, &WxHeartBeatReq{}},
		{"client login", RoleClient, CmdClientLogin, &WxLoginReq{}},
		{"client join", RoleClient, CmdClientJoinRoom, &WxJoinRoomReq{}},
		{"client quit", RoleClient, CmdClientQuitRoom, &WxQuitRoomReq{}},
		{"client sync", RoleClient, CmdClientSyncMessage, &WxSyncMessageReq{}},
		{"client debug", RoleClient, CmdClientSendDebug, &SendDebugMessageReq{}},
		{"client parallel debug", RoleClient, CmdClientSendDebugParallel, &NewSendDebugMessageReq{}},
		{"client notify", RoleClient, CmdClientMessageNotify, &MessageNotify{}},
		{"client parallel notify", RoleClient, CmdClientMessageNotifyParallel, &MessageNotify{}},
		{"developer heartbeat", RoleDeveloper, CmdDevHeartbeat, &DevHeartBeatReq{}},
		{"developer login", RoleDeveloper, CmdDevLogin, &DevLoginReq{}},
		{"developer join", RoleDeveloper, CmdDevJoinRoom, &DevJoinRoomReq{}},
		{"developer quit", RoleDeveloper, CmdDevQuitRoom, &DevQuitRoomReq{}},
		{"developer sync", RoleDeveloper, CmdDevSyncMessage, &DevSyncMessageReq{}},
		{"developer debug", RoleDeveloper, CmdDevSendDebug, &SendDebugMessageReq{}},
		{"developer parallel debug", RoleDeveloper, CmdDevSendDebugParallel, &NewSendDebugMessageReq{}},
		{"developer notify", RoleDeveloper, CmdDevMessageNotify, &MessageNotify{}},
		{"developer parallel notify", RoleDeveloper, CmdDevMessageNotifyParallel, &MessageNotify{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodeRequestPayload(tc.role, tc.cmd, nil)
			if err != nil {
				t.Fatal(err)
			}
			if reflect.TypeOf(got) != reflect.TypeOf(tc.want) {
				t.Fatalf("type=%T want %T", got, tc.want)
			}
		})
	}
}

func TestDecodeResponsePayloadByEndpoint(t *testing.T) {
	tests := []struct {
		name string
		role EndpointRole
		cmd  uint32
		want any
	}{
		{"client heartbeat", RoleClient, CmdClientHeartbeat, &WxHeartBeatResp{}},
		{"client login", RoleClient, CmdClientLogin, &WxLoginResp{}},
		{"client join", RoleClient, CmdClientJoinRoom, &WxJoinRoomResp{}},
		{"client quit", RoleClient, CmdClientQuitRoom, &WxQuitRoomResp{}},
		{"client sync", RoleClient, CmdClientSyncMessage, &WxSyncMessageResp{}},
		{"client debug", RoleClient, CmdClientSendDebug, &SendDebugMessageResp{}},
		{"client parallel debug", RoleClient, CmdClientSendDebugParallel, &NewSendDebugMessageResp{}},
		{"developer heartbeat", RoleDeveloper, CmdDevHeartbeat, &DevHeartBeatResp{}},
		{"developer login", RoleDeveloper, CmdDevLogin, &DevLoginResp{}},
		{"developer join", RoleDeveloper, CmdDevJoinRoom, &DevJoinRoomResp{}},
		{"developer quit", RoleDeveloper, CmdDevQuitRoom, &DevQuitRoomResp{}},
		{"developer sync", RoleDeveloper, CmdDevSyncMessage, &DevSyncMessageResp{}},
		{"developer debug", RoleDeveloper, CmdDevSendDebug, &SendDebugMessageResp{}},
		{"developer parallel debug", RoleDeveloper, CmdDevSendDebugParallel, &NewSendDebugMessageResp{}},
		{"event begin", RoleDeveloper, CmdEventBegin, &EventNotify{}},
		{"event end", RoleDeveloper, CmdEventEnd, &EventNotify{}},
		{"event block", RoleDeveloper, CmdEventBlock, &EventNotify{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodeResponsePayload(tc.role, tc.cmd, nil)
			if err != nil {
				t.Fatal(err)
			}
			if reflect.TypeOf(got) != reflect.TypeOf(tc.want) {
				t.Fatalf("type=%T want %T", got, tc.want)
			}
		})
	}
}

func TestDecodePayloadRejectsUnknownCommand(t *testing.T) {
	if _, err := DecodeRequestPayload(RoleClient, 9999, nil); err == nil {
		t.Fatal("request accepted unknown command")
	}
	if _, err := DecodeResponsePayload(RoleDeveloper, 9999, nil); err == nil {
		t.Fatal("response accepted unknown command")
	}
}
