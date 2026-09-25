package playbook

import "testing"

func TestAgentMockStaysOffUnlessEnabled(t *testing.T) {
	t.Setenv("XUNTAI_AGENT_MOCK", "")
	if AgentMock() {
		t.Fatal("空值打开了模拟回写")
	}
	t.Setenv("XUNTAI_AGENT_MOCK", "off")
	if AgentMock() {
		t.Fatal("off 打开了模拟回写")
	}
	t.Setenv("XUNTAI_AGENT_MOCK", "1")
	if !AgentMock() {
		t.Fatal("1 没有打开模拟回写")
	}
}
