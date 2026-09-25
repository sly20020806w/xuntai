package playbook

import "testing"

func TestTaskMockStaysOffUnlessEnabled(t *testing.T) {
	t.Setenv("XUNTAI_TASK_MOCK", "")
	if TaskMock() {
		t.Fatal("空值打开了模拟回写")
	}
	t.Setenv("XUNTAI_TASK_MOCK", "off")
	if TaskMock() {
		t.Fatal("off 打开了模拟回写")
	}
	t.Setenv("XUNTAI_TASK_MOCK", "1")
	if !TaskMock() {
		t.Fatal("1 没有打开模拟回写")
	}
}
