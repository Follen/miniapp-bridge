package process

import "testing"

func TestParseVersion(t *testing.T) {
	if ParseVersion("WeChatAppEx-19027.exe") != 19027 {
		t.Fatal()
	}
}

func TestSelectParentUsesReferenceStableTieOrder(t *testing.T) {
	ps := []Process{{Name: "WeChatAppEx.exe", ParentPID: 20}, {Name: "WeChatAppEx.exe", ParentPID: 10}}
	if got, _ := SelectParent(ps, "WeChatAppEx.exe"); got != 10 { t.Fatalf("got %d want last equal-frequency parent 10", got) }
}

func TestSelectParent(t *testing.T) {
	ps := []Process{{Name: "WeChatAppEx.exe", ParentPID: 10}, {Name: "WeChatAppEx.exe", ParentPID: 10}, {Name: "WeChatAppEx.exe", ParentPID: 20}}
	if got, _ := SelectParent(ps, "WeChatAppEx.exe"); got != 10 {
		t.Fatal(got)
	}
}
