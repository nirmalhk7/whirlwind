package execution

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type offlineACPFixtureTransport func(*http.Request) (*http.Response, error)

func (fn offlineACPFixtureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestOfflineACPCleanupRequiresDaemonOwnershipAndExit(t *testing.T) {
	token := strings.Repeat("a", 64)
	record := offlineACPRecord{
		Name: offlineACPNamePrefix + token[:24], Token: token,
		ID: strings.Repeat("b", 64), Image: "sha256:" + strings.Repeat("c", 64), Daemon: "trusted-daemon",
	}
	for _, scenario := range []string{"removed", "wrong owner", "wrong daemon", "running after stop", "removal unconfirmed", "absent live"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			if err := saveOfflineACPRecord(root, record); err != nil {
				t.Fatal(err)
			}
			var calls []string
			var container offlineACPContainer
			container.ID, container.Name, container.Image = record.ID, "/"+record.Name, record.Image
			container.Config.Labels = map[string]string{offlineACPOwnerLabel: record.Token}
			container.State.Status = "exited"
			if scenario == "wrong owner" {
				container.Config.Labels[offlineACPOwnerLabel] = strings.Repeat("d", 64)
			}
			if scenario == "running after stop" {
				container.State.Status, container.State.Running, container.State.Pid = "running", true, 123
			}
			body, _ := json.Marshal(container)
			deleted := false
			api := offlineACPAPI{daemon: record.Daemon, client: &http.Client{Transport: offlineACPFixtureTransport(func(req *http.Request) (*http.Response, error) {
				calls = append(calls, req.Method)
				status := http.StatusOK
				switch req.Method {
				case http.MethodDelete:
					deleted = true
					status = http.StatusNoContent
				case http.MethodPost:
					status = http.StatusNoContent
				case http.MethodGet:
					if (deleted && scenario != "removal unconfirmed") || scenario == "absent live" {
						status = http.StatusNotFound
					}
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
			})}}
			if scenario == "wrong daemon" {
				api.daemon = "different-daemon"
			}
			_, err := api.reap(context.Background(), root, record, false)
			_, statErr := os.Stat(filepath.Join(root, record.Name+".json"))
			if scenario == "removed" {
				if err != nil || !errors.Is(statErr, os.ErrNotExist) || !slices.Equal(calls, []string{"GET", "DELETE", "GET", "GET"}) {
					t.Fatalf("cleanup: %v, journal: %v, calls: %v", err, statErr, calls)
				}
				return
			}
			if err == nil || statErr != nil {
				t.Fatalf("uncertain cleanup accepted: %v, journal: %v", err, statErr)
			}
			if scenario != "removal unconfirmed" && slices.Contains(calls, "DELETE") {
				t.Fatalf("removed unverified object: %v", calls)
			}
		})
	}
}

func TestOfflineACPRecoveryRefusesUnknownCreateOutcome(t *testing.T) {
	root := t.TempDir()
	token := strings.Repeat("a", 64)
	record := offlineACPRecord{Name: offlineACPNamePrefix + token[:24], Token: token, Image: "sha256:" + strings.Repeat("b", 64), Daemon: "trusted-daemon"}
	if err := saveOfflineACPRecord(root, record); err != nil {
		t.Fatal(err)
	}
	api := offlineACPAPI{daemon: record.Daemon, client: &http.Client{Transport: offlineACPFixtureTransport(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet {
			t.Fatalf("unexpected recovery request %s", req.Method)
		}
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}}
	if _, err := api.reap(context.Background(), root, record, true); err == nil {
		t.Fatal("accepted ambiguous create")
	}
	if err := CheckOfflineACPRecovery(root); err == nil {
		t.Fatal("ambiguous record disappeared")
	}
}

func TestOfflineACPRejectsImplicitOptOutAndWritableSource(t *testing.T) {
	t.Setenv("WHIRLWIND_OFFLINE_ACP_FIXTURE", "/operator/fixture")
	command := []string{"/operator/fixture"}
	for _, profile := range []Profile{
		{ReadOnly: true},
		{direct: true, ReadOnly: false},
		{direct: true, ReadOnly: true},
	} {
		t.Setenv("WHIRLWIND_OFFLINE_ACP_CHECKOUT", "")
		if err := profile.selectOfflineACP(command, nil); err == nil || profile.OfflineACP() {
			t.Fatal("fixture selection did not fail closed")
		}
	}
}
