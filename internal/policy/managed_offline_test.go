package policy

import (
	"strings"
	"testing"
)

func TestManagedOfflinePolicyRequiresDigestAndBoundedChecks(t *testing.T) {
	const settings = "allowed_paths: ['**']\nrisk_rules: [{level: low, paths: ['**']}]\nchecks: [{name: test, command: [go, test, ./...]}]\n"
	const pinned = "offline_docker_image: test-image@sha256:"
	content := pinned + strings.Repeat("a", 64) + "\n" + settings
	policy, hash, err := ParseManagedPolicy("project-a", content)
	if err != nil || policy.OfflineDockerImage == "" || len(hash) != 64 {
		t.Fatalf("approved offline policy was not accepted: %v", err)
	}
	_, scoped, err := ParseManagedPolicy("project-b", content)
	if err != nil || hash == scoped {
		t.Fatal("policy approval was not bound to the project")
	}
	for _, bad := range []string{
		"offline_docker_image: test-image:latest\n" + settings,
		content + "quality: {}\n",
		strings.Replace(content, "command: [go, test, ./...]", "required: false, command: [go, test, ./...]", 1),
		strings.Replace(content, "command: [go, test, ./...]", "command: [qlty, check]", 1),
	} {
		if _, _, err := ParseManagedPolicy("project-a", bad); err == nil {
			t.Fatalf("accepted unsupported offline configuration: %s", bad)
		}
	}
}
