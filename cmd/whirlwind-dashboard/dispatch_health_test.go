package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"whirlwind/internal/control"
	"whirlwind/internal/model"
)

type dispatchHealthCall func(context.Context, control.Request) (control.Response, error)

func (call dispatchHealthCall) Call(ctx context.Context, request control.Request) (control.Response, error) {
	return call(ctx, request)
}

func TestLoadDispatchHealthGatesProviderProbesAndHandlesLegacy(t *testing.T) {
	for _, test := range []dispatchHealthFixture{
		{name: "pending"},
		{name: "blocked", result: model.DispatchHealth{Checked: true, BlockReason: "invalid runtime identity"}},
		{name: "healthy", result: model.DispatchHealth{Checked: true}, wantProbes: true},
		{name: "old supervisor", callErr: fmt.Errorf("unsupported local operation %q", control.DispatchHealthOperation), wantProbes: true},
		{name: "failed inspection", callErr: errors.New("unexpected response"), wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertDispatchHealthFixture(t, test)
		})
	}
}

type dispatchHealthFixture struct {
	name                string
	result              model.DispatchHealth
	callErr             error
	wantProbes, wantErr bool
}

func assertDispatchHealthFixture(t *testing.T, test dispatchHealthFixture) {
	t.Helper()
	snapshot := control.Snapshot{}
	allowed, err := loadDispatchHealth(t.Context(), dispatchHealthCall(func(_ context.Context, request control.Request) (control.Response, error) {
		if request.Operation != control.DispatchHealthOperation {
			t.Fatalf("unexpected inspection operation: %+v", request)
		}
		return control.Response{RequestID: request.RequestID, Result: test.result}, test.callErr
	}), snapshot)
	if allowed != test.wantProbes || (err != nil) != test.wantErr {
		t.Fatalf("provider probes allowed=%t and error=%v", allowed, err)
	}
	if test.wantErr {
		if len(snapshot) != 0 {
			t.Fatal("unknown health was reported as verified")
		}
		return
	}
	var observed model.DispatchHealth
	rows := snapshot[control.DispatchHealthSnapshotKey]
	if len(rows) != 1 || json.Unmarshal(rows[0].Data, &observed) != nil || observed != test.result {
		t.Fatalf("dashboard did not receive exact recovery status: %v", rows)
	}
}
