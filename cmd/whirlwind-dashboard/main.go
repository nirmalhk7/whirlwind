package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"whirlwind/internal/control"
	"whirlwind/internal/dashboard"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func main() {
	if err := run(); err != nil {
		_, _ = os.Stderr.WriteString("Whirlwind dashboard: " + err.Error() + "\n")
		os.Exit(1)
	}
}

func run() error {
	data := os.Getenv("WHIRLWIND_DATA_DIR")
	if data == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		data = filepath.Join(home, ".local", "share", "whirlwind", "parity-v1")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	app := dashboard.New(data)
	client := control.NewClient(control.SocketPath(data))
	app.SetCommandHandler(func(ctx context.Context, request control.Request) (json.RawMessage, error) {
		response, err := client.Call(ctx, request)
		if err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(response.Result)
		return encoded, err
	})
	done := app.Watch(ctx, func(ctx context.Context) (control.Snapshot, error) { return loadSnapshot(ctx, data) })
	err := app.Run()
	cancel()
	<-done
	return err
}

func loadSnapshot(ctx context.Context, data string) (control.Snapshot, error) {
	controlPath := control.SocketPath(data)
	if _, err := os.Stat(controlPath); err == nil {
		snapshot, started, callErr := loadOnlineSnapshot(ctx, control.NewClient(controlPath))
		if callErr == nil {
			loadProviderStatuses(ctx, control.NewClient(controlPath), snapshot)
			return finishOnlineSnapshot(ctx, data, snapshot)
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if started || !strings.HasPrefix(callErr.Error(), "connect to local supervisor at ") {
			return nil, callErr
		}
		// A stale socket that cannot be connected to does not prevent safe
		// database-only inspection below.
	}
	path := filepath.Join(data, "whirlwind.db")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return markSupervisor(control.Snapshot{}, false), nil
	} else if err != nil {
		return nil, err
	}
	reader, err := store.OpenReadOnly(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = reader.Close() }()
	snapshot, err := (control.SupervisorDispatcher{DB: reader.DB}).Snapshot(ctx, 100)
	if err != nil {
		return nil, err
	}
	return markSupervisor(snapshot, false), nil
}

func loadOnlineSnapshot(ctx context.Context, client *control.Client) (control.Snapshot, bool, error) {
	snapshot := control.Snapshot{}
	started, err := completeOnlineSnapshot(ctx, client, snapshot)
	return snapshot, started, err
}

func completeOnlineSnapshot(ctx context.Context, client *control.Client, snapshot control.Snapshot) (bool, error) {
	started := false
	for _, kind := range []string{"projects", "execution_plans", "ideas", "operations", "directions", "sessions", "questions", "permissions", "verification", "reviews", "inbox", "plan_versions", "agents", "providers", "project_status", "settings"} {
		read, err := completeOnlineRecords(ctx, client, snapshot, kind)
		started = started || read
		if err != nil {
			return started, fmt.Errorf("inspect %s: %w", kind, err)
		}
	}
	return started, completeOnlineEvents(ctx, client, snapshot)
}

func completeOnlineEvents(ctx context.Context, client *control.Client, snapshot control.Snapshot) error {
	var cursor int64
	for {
		request := control.Request{RequestID: fmt.Sprintf("dashboard-events-%d", cursor), Operation: "inspect.events", EventCursor: cursor, Limit: 100}
		response, err := dashboardCall(ctx, client, request)
		if err != nil {
			return fmt.Errorf("inspect events: %w", err)
		}
		encoded, err := json.Marshal(response.Result)
		if err != nil {
			return err
		}
		var page []model.Event
		if err := json.Unmarshal(encoded, &page); err != nil {
			return fmt.Errorf("decode event inspection page: %w", err)
		}
		if len(page) == 0 {
			return nil
		}
		if page[len(page)-1].ID <= cursor {
			return errors.New("event inspection cursor did not advance")
		}
		for _, event := range page {
			if event.ID <= cursor {
				return errors.New("event inspection cursor did not advance")
			}
			data, err := json.Marshal(event)
			if err != nil {
				return err
			}
			snapshot["events"] = append(snapshot["events"], model.Record{ID: strconv.FormatInt(event.ID, 10), Data: data, UpdatedAt: event.CreatedAt})
			cursor = event.ID
		}
	}
}

func dashboardCall(ctx context.Context, client *control.Client, request control.Request) (control.Response, error) {
	callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return client.Call(callCtx, request)
}

func markSupervisor(snapshot control.Snapshot, online bool) control.Snapshot {
	data, _ := json.Marshal(map[string]bool{"online": online})
	snapshot["supervisor"] = []model.Record{{ID: "local", Data: data}}
	return snapshot
}
