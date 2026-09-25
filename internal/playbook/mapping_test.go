package playbook

import "testing"

func TestMappingLiteralsAndMissingKey(t *testing.T) {
	got, err := applyMapping(`{"title":"固定","node":"{{ run.input.tree_node_id }}"}`, map[string]any{
		"tree_node_id": float64(5),
	}, map[string]any{}, map[string]map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if got["title"] != "固定" {
		t.Fatalf("字面量 = %v", got["title"])
	}
	id, ok := asUint(got["node"])
	if !ok || id != 5 {
		t.Fatalf("节点 = %v", got["node"])
	}
	_, err = applyMapping(`{"tag":"{{ run.input.image_tag }}"}`, map[string]any{}, nil, nil)
	if err == nil || err.Error() != "找不到参数 run.input.image_tag" {
		t.Fatalf("缺参数 = %v", err)
	}
	_, err = applyMapping(`{"who":"{{ steps.gate.output.approved_by }}"}`, nil, nil, map[string]map[string]any{
		"gate": {"ticket_id": float64(1)},
	})
	if err == nil || err.Error() != "找不到参数 steps.gate.output.approved_by" {
		t.Fatalf("缺输出 = %v", err)
	}
}
