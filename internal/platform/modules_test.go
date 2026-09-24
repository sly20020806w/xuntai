package platform

import "testing"

func TestModules(t *testing.T) {
	want := []string{"base", "tree", "ticket", "task", "monitor", "k8s", "cicd", "db"}
	if len(Modules) != len(want) {
		t.Fatalf("模块数量 = %d, 期望 %d", len(Modules), len(want))
	}
	for i := range want {
		if Modules[i] != want[i] {
			t.Fatalf("第 %d 个模块 = %s, 期望 %s", i+1, Modules[i], want[i])
		}
	}
}
