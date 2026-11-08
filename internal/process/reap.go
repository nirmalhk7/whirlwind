//go:build darwin || linux

package process

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Reap verifies registered and tagged orphan exit before discarding recovery
// evidence. Call only while dispatch is stopped and the state-directory lock is held.
func Reap(root string) error {
	records, err := registryRecords(root)
	if err != nil {
		return err
	}
	processes, err := processSnapshot()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrCleanup, err)
	}
	for _, process := range processes {
		if process.Registry == root && validToken(process.Token) {
			if _, exists := records[process.Token]; !exists {
				records[process.Token] = registration{Token: process.Token}
			}
		}
	}
	for _, record := range records {
		if err := cleanupRegistration(record); err != nil {
			return err
		}
		if err := os.Remove(filepath.Join(root, record.Token+".json")); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func registryRecords(root string) (map[string]registration, error) {
	files, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return map[string]registration{}, nil
	}
	if err != nil {
		return nil, err
	}
	records := map[string]registration{}
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		record, err := readRegistration(filepath.Join(root, file.Name()))
		if err != nil {
			return nil, err
		}
		if file.Name() != record.Token+".json" {
			return nil, errors.New("process registration filename does not match ownership token")
		}
		records[record.Token] = record
	}
	return records, nil
}

func readRegistration(path string) (registration, error) {
	var record registration
	body, err := os.ReadFile(path)
	if err != nil {
		return record, err
	}
	if json.Unmarshal(body, &record) != nil || record.PID < 0 || record.PID == 1 || !validToken(record.Token) {
		return record, errors.New("invalid orphan process registration")
	}
	return record, nil
}

// Inspect is read-only. Clean means no recovery records and no tagged live
// processes for this registry; unavailable kernel evidence returns an error.
func Inspect(root string) (Report, error) {
	report := Report{Processes: []Identity{}}
	if root == "" {
		return report, errors.New("process registry is not configured")
	}
	records, err := registryRecords(root)
	if err != nil {
		return report, err
	}
	report.Registrations = len(records)
	processes, err := processSnapshot()
	if err != nil {
		return report, err
	}
	owned := map[int]Identity{}
	for _, record := range records {
		for _, identity := range liveProcesses(record, processes) {
			owned[identity.PID] = identity
		}
	}
	for _, process := range processes {
		if process.Registry == root && validToken(process.Token) && !process.Zombie {
			owned[process.PID] = process.Identity
		}
	}
	for _, identity := range owned {
		report.Processes = append(report.Processes, identity)
	}
	sort.Slice(report.Processes, func(i, j int) bool { return report.Processes[i].PID < report.Processes[j].PID })
	report.Clean = report.Registrations == 0 && len(report.Processes) == 0
	return report, nil
}
