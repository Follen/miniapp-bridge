package wmpf

import (
	"bytes"
	"testing"
)

func TestMarshalNestedRepeatedAndFixed(t *testing.T) {
	in := SetupContext{RegisterInterface: &RegisterInterface{ObjName: "wx"}, DeviceInfo: &DeviceInfo{ScreenWidth: 375.5, PixelRatio: 2}, ConfigureJs: "let x=1"}
	b, err := MarshalMessage(in)
	if err != nil {
		t.Fatal(err)
	}
	var out SetupContext
	if err := UnmarshalMessage(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.RegisterInterface == nil || out.RegisterInterface.ObjName != "wx" || out.DeviceInfo == nil || out.DeviceInfo.ScreenWidth != 375.5 || out.ConfigureJs != "let x=1" {
		t.Fatalf("roundtrip: %+v", out)
	}
	rep := CallInterfaceProto{ObjName: "o", MethodName: "m", MethodArgs: []string{"a", "b"}, CallId: 3}
	rb, err := MarshalMessage(rep)
	if err != nil {
		t.Fatal(err)
	}
	var rout CallInterfaceProto
	if err := UnmarshalMessage(rb, &rout); err != nil {
		t.Fatal(err)
	}
	if len(rout.MethodArgs) != 2 || rout.MethodArgs[1] != "b" {
		t.Fatalf("repeated: %+v", rout)
	}
}

func TestMarshalPreservesSignedVarintAndUnknownGeneric(t *testing.T) {
	b, err := MarshalMessage(BaseResp{Errcode: -1, Errmsg: "bad"})
	if err != nil {
		t.Fatal(err)
	}
	var out BaseResp
	if err := UnmarshalMessage(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.Errcode != -1 || out.Errmsg != "bad" {
		t.Fatalf("signed: %+v", out)
	}
	raw := []byte{0x08, 0x01, 0x78, 0x01}
	m, err := DecodeGeneric("WARemoteDebug_DebugMessage", raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(EncodeGeneric(m), raw) {
		t.Fatalf("unknown field was not retained: %x", EncodeGeneric(m))
	}
}

func TestRepeatedNestedMessagesPreserveOrder(t *testing.T) {
	in := MessageNotify{DebugMessageList: []DebugMessageProto{{Seq: 1, Category: CategoryPing}, {Seq: 2, Category: CategoryChromeDevtools}}}
	b, err := MarshalMessage(in)
	if err != nil {
		t.Fatal(err)
	}
	var out MessageNotify
	if err := UnmarshalMessage(b, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.DebugMessageList) != 2 || out.DebugMessageList[0].Seq != 1 || out.DebugMessageList[1].Seq != 2 {
		t.Fatalf("order lost: %+v", out.DebugMessageList)
	}
	setup := SetupContext{RegisterInterface: &RegisterInterface{ObjName: "wx", ObjMethodList: []MethodWithArgs{{MethodName: "a", MethodArgList: []string{"x", "y"}}, {MethodName: "b"}}}}
	b, err = MarshalMessage(setup)
	if err != nil {
		t.Fatal(err)
	}
	var decoded SetupContext
	if err := UnmarshalMessage(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.RegisterInterface == nil || len(decoded.RegisterInterface.ObjMethodList) != 2 || len(decoded.RegisterInterface.ObjMethodList[0].MethodArgList) != 2 {
		t.Fatalf("nested repeated lost: %+v", decoded.RegisterInterface)
	}
}
