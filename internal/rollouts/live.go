package rollouts

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Document 是 Argo Rollouts 的 Rollout 对象。当前阶段只放一个 setWeight，暂停时长来自发布项。
func Document(name, image string, replicas, weight, stableSeconds int) ([]byte, error) {
	if replicas <= 0 {
		replicas = 1
	}
	pause := map[string]any{}
	if stableSeconds > 0 {
		pause["duration"] = fmt.Sprintf("%ds", stableSeconds)
	}
	doc := map[string]any{
		"apiVersion": "argoproj.io/v1alpha1",
		"kind":       "Rollout",
		"metadata":   map[string]any{"name": name},
		"spec": map[string]any{
			"replicas": replicas,
			"selector": map[string]any{"matchLabels": map[string]string{"app": name}},
			"strategy": map[string]any{"canary": map[string]any{"steps": []any{
				map[string]any{"setWeight": weight},
				map[string]any{"pause": pause},
			}}},
			"template": map[string]any{
				"metadata": map[string]any{"labels": map[string]string{"app": name}},
				"spec": map[string]any{"containers": []any{
					map[string]any{"name": name, "image": image},
				}},
			},
		},
	}
	return json.Marshal(doc)
}

// Apply 按官方 CRD 路径创建或更新 Rollout。没有 kubeconfig 时直接失败，不会改走模拟。
func Apply(kubeconfig, namespace, name, image string, replicas, weight, stableSeconds int) error {
	if strings.TrimSpace(kubeconfig) == "" {
		return errors.New("集群还没有可用的 kubeconfig")
	}
	cfg, err := parseKube(kubeconfig)
	if err != nil {
		return err
	}
	if namespace == "" {
		namespace = "default"
	}
	body, err := Document(name, image, replicas, weight, stableSeconds)
	if err != nil {
		return err
	}
	client := cfg.client()
	base := strings.TrimRight(cfg.Server, "/") + "/apis/argoproj.io/v1alpha1/namespaces/" + namespace + "/rollouts"
	status, err := send(client, http.MethodPut, base+"/"+name, cfg.Token, body)
	if err != nil {
		return err
	}
	if status == http.StatusNotFound || status == http.StatusMethodNotAllowed {
		status, err = send(client, http.MethodPost, base, cfg.Token, body)
		if err != nil {
			return err
		}
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("Rollout 接口返回 %d", status)
	}
	return nil
}

type kubeConfig struct {
	Server string
	Token  string
	TLS    *tls.Config
}

func parseKube(raw string) (kubeConfig, error) {
	var file struct {
		Current  string `yaml:"current-context"`
		Clusters []struct {
			Name    string `yaml:"name"`
			Cluster struct {
				Server   string `yaml:"server"`
				Insecure bool   `yaml:"insecure-skip-tls-verify"`
			} `yaml:"cluster"`
		} `yaml:"clusters"`
		Users []struct {
			Name string `yaml:"name"`
			User struct {
				Token string `yaml:"token"`
			} `yaml:"user"`
		} `yaml:"users"`
		Contexts []struct {
			Name    string `yaml:"name"`
			Context struct {
				Cluster string `yaml:"cluster"`
				User    string `yaml:"user"`
			} `yaml:"context"`
		} `yaml:"contexts"`
	}
	if err := yaml.Unmarshal([]byte(raw), &file); err != nil {
		return kubeConfig{}, errors.New("集群还没有可用的 kubeconfig")
	}
	var clusterName, userName string
	for _, item := range file.Contexts {
		if item.Name == file.Current || file.Current == "" {
			clusterName = item.Context.Cluster
			userName = item.Context.User
			break
		}
	}
	var cfg kubeConfig
	for _, item := range file.Clusters {
		if item.Name == clusterName || clusterName == "" {
			cfg.Server = strings.TrimSpace(item.Cluster.Server)
			if item.Cluster.Insecure {
				cfg.TLS = &tls.Config{InsecureSkipVerify: true}
			}
			break
		}
	}
	for _, item := range file.Users {
		if item.Name == userName || userName == "" {
			cfg.Token = item.User.Token
			break
		}
	}
	if cfg.Server == "" {
		return kubeConfig{}, errors.New("集群还没有可用的 kubeconfig")
	}
	return cfg, nil
}

func (k kubeConfig) client() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if k.TLS != nil {
		transport.TLSClientConfig = k.TLS
	}
	return &http.Client{Timeout: 5 * time.Second, Transport: transport}
}

func send(client *http.Client, method, url, token string, body []byte) (int, error) {
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("Rollout 接口没有连上")
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1024))
	return res.StatusCode, nil
}
