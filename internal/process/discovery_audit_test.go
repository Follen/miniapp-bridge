package process

import "testing"

func TestAuditParseVersionUsesLastNumericPathComponent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path string
		want int
	}{
		{`C:\Users\2026\AppData\Roaming\Tencent\WMPF\25297\WeChatAppEx.exe`, 25297},
		{`C:\Program Files (x86)\Tencent\WMPF\19027\WeChatAppEx.exe`, 19027},
		{`C:\WMPF\build-19027\patch-25297\WeChatAppEx.exe`, 25297},
		{`C:\WMPF\release-1234567\WeChatAppEx.exe`, 1234567},
	}
	for _, tc := range cases {
		if got := ParseVersion(tc.path); got != tc.want {
			t.Errorf("ParseVersion(%q)=%d, want last numeric group %d", tc.path, got, tc.want)
		}
	}
}

func TestAuditSelectParentMatchesReferenceFrequencyAndStableTie(t *testing.T) {
	t.Parallel()
	processes := []Process{
		{Name: "WeChatAppEx.exe", ParentPID: 40},
		{Name: "unrelated.exe", ParentPID: 99},
		{Name: "WeChatAppEx.exe", ParentPID: 20},
		{Name: "WeChatAppEx.exe", ParentPID: 40},
		{Name: "WeChatAppEx.exe", ParentPID: 20},
	}
	got, err := SelectParent(processes, "WeChatAppEx.exe")
	if err != nil {
		t.Fatal(err)
	}
	// Array.sort is stable in the pinned Node runtime; pop therefore picks the
	// final parent among equal-frequency candidates.
	if got != 20 {
		t.Fatalf("selected parent=%d, want 20", got)
	}
}

func TestAuditSelectParentUsesReferenceExactProcessName(t *testing.T) {
	t.Parallel()
	processes := []Process{
		{Name: "WeChatAppEx.exe", ParentPID: 10},
		{Name: "wechatappex.exe", ParentPID: 20},
		{Name: "wechatappex.exe", ParentPID: 20},
	}
	got, err := SelectParent(processes, "WeChatAppEx.exe")
	if err != nil {
		t.Fatal(err)
	}
	if got != 10 {
		t.Fatalf("selected parent=%d, want exact-name parent 10", got)
	}
}

func TestAuditSelectParentRejectsMissingOrZeroParent(t *testing.T) {
	t.Parallel()
	for _, processes := range [][]Process{
		nil,
		{{Name: "unrelated.exe", ParentPID: 10}},
		{{Name: "WeChatAppEx.exe", ParentPID: 0}},
	} {
		if pid, err := SelectParent(processes, "WeChatAppEx.exe"); err == nil || pid != 0 {
			t.Errorf("SelectParent(%v) pid=%d err=%v, want zero/error", processes, pid, err)
		}
	}
}
