package rollouts

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDocumentSetsWeight(t *testing.T) {
	raw, err := Document("order-api", "order-api:1.9.5", 6, 10, 5)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, piece := range []string{`"apiVersion":"argoproj.io/v1alpha1"`, `"kind":"Rollout"`, `"setWeight":10`, `"duration":"5s"`, `"image":"order-api:1.9.5"`} {
		if !strings.Contains(text, piece) {
			t.Fatalf("Rollout 对象缺少 %s\n%s", piece, text)
		}
	}
}

func TestApplyUsesRolloutAPI(t *testing.T) {
	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = r.Method + " " + r.URL.Path + "\n" + string(body)
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("认证 = %s", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	raw := "apiVersion: v1\nkind: Config\ncurrent-context: c\nclusters:\n- name: local\n  cluster:\n    server: " + server.URL + "\nusers:\n- name: u\n  user:\n    token: token\ncontexts:\n- name: c\n  context:\n    cluster: local\n    user: u\n"
	if err := Apply(raw, "default", "order-api", "order-api:1.9.5", 2, 50, 0); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "/apis/argoproj.io/v1alpha1/namespaces/default/rollouts/order-api") || !strings.Contains(got, `"setWeight":50`) {
		t.Fatalf("请求 = %s", got)
	}
}

func TestApplyRejectsEmptyKubeconfig(t *testing.T) {
	Reset(9)
	Remember(9, Stage{Weight: 10})
	if err := Apply("", "default", "order-api", "order-api:1", 1, 10, 0); err == nil || !strings.Contains(err.Error(), "kubeconfig") {
		t.Fatalf("空配置 = %v", err)
	}
	if Current(9).Weight != 10 {
		t.Fatal("真实路径失败时不该改模拟进度")
	}
}
