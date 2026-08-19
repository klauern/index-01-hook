package main

import (
	"os"
	"strings"
	"testing"
)

func TestPublicValidationCleanupAndIsolation(t *testing.T) {
	contents, err := os.ReadFile("scripts/public-experience-e2e.sh")
	if err != nil {
		t.Fatalf("read E2E validation script: %v", err)
	}
	text := string(contents)
	for _, required := range []string{
		"Docker endpoint is not local",
		"generated fixture image already exists",
		"generated application image already exists",
		"generated Compose network already exists",
		"docker volume inspect \"$db_volume\" >/dev/null 2>&1 && cleanup_failed=1",
		"docker network inspect \"$network\" >/dev/null 2>&1 && cleanup_failed=1",
		"docker image inspect \"$fixture_image\" >/dev/null 2>&1 && cleanup_failed=1",
		"docker image inspect \"$app_image\" >/dev/null 2>&1 && cleanup_failed=1",
		"[ ! -e \"$temporary_root\" ] || cleanup_failed=1",
		"public experience E2E cleanup failed",
		"application accepted an untrusted provider certificate",
		"fixture publishes a host port",
		"application publishes a host port",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("E2E validation does not contain %q", required)
		}
	}

	compose, err := os.ReadFile("e2e/compose.yaml")
	if err != nil {
		t.Fatalf("read E2E Compose file: %v", err)
	}
	composeText := string(compose)
	if strings.Count(composeText, "internal: true") != 2 {
		t.Error("E2E Compose file must define two internal networks")
	}
	if !strings.Contains(composeText, "host_ip: 127.0.0.1") {
		t.Error("E2E Caddy port is not loopback-bound")
	}
}

func TestPublicInfrastructureOwnsOnlyGeneratedCluster(t *testing.T) {
	contents, err := os.ReadFile("scripts/public-infrastructure_test.sh")
	if err != nil {
		t.Fatalf("read infrastructure validation script: %v", err)
	}
	text := string(contents)
	collision := strings.Index(text, "generated kind cluster name already exists")
	owned := strings.Index(text, "cluster_created=1\nrun_step kind-create")
	create := strings.Index(text, "kind create cluster")
	if collision < 0 || owned <= collision || create <= owned {
		t.Error("Kind cluster ownership is not established after collision checks and before creation")
	}
	for _, required := range []string{
		"generated application image already exists",
		"docker image rm -f \"$application_image\"",
		"docker image inspect \"$application_image\" >/dev/null 2>&1 && status=1",
		"label=io.x-k8s.kind.cluster=$cluster_name",
		"[ ! -e \"$test_root\" ] || status=1",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("infrastructure validation does not contain %q", required)
		}
	}
}

func TestKubernetesHTTPSPolicyExcludesSpecialUseRanges(t *testing.T) {
	contents, err := os.ReadFile("deploy/kubernetes/network-policy.yaml")
	if err != nil {
		t.Fatalf("read Kubernetes NetworkPolicy: %v", err)
	}
	policy := string(contents)
	for _, network := range []string{
		"0.0.0.0/8",
		"10.0.0.0/8",
		"100.64.0.0/10",
		"127.0.0.0/8",
		"169.254.0.0/16",
		"172.16.0.0/12",
		"192.0.2.0/24",
		"192.31.196.0/24",
		"192.52.193.0/24",
		"192.88.99.0/24",
		"192.168.0.0/16",
		"192.175.48.0/24",
		"198.18.0.0/15",
		"198.51.100.0/24",
		"203.0.113.0/24",
		"224.0.0.0/4",
		"240.0.0.0/4",
		"::/128",
		"::1/128",
		"64:ff9b:1::/48",
		"100::/64",
		"2001::/23",
		"2001:db8::/32",
		"2002::/16",
		"2620:4f:8000::/48",
		"3fff::/20",
		"5f00::/16",
		"fc00::/7",
		"fe80::/10",
		"ff00::/8",
	} {
		if !strings.Contains(policy, "- "+network) {
			t.Errorf("Kubernetes HTTPS policy does not exclude %s", network)
		}
	}
}
