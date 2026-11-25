/*
Copyright 2026 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewTPUHealthCheckerClonesDevices(t *testing.T) {
	src := AllocatableDevices{
		"dev0": &AllocatableDevice{UUID: "dev0", allocatable: true},
	}
	hc := NewTPUHealthChecker(src, nil, "/dev", "v6e", nil)

	// Mutating the checker's copy must not affect the source map.
	d := hc.devices["dev0"]
	d.allocatable = false
	hc.devices["dev0"] = d

	if !src["dev0"].allocatable {
		t.Error("expected source device to be unchanged after mutating checker copy")
	}
}

func TestDeviceExists(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "vfio0"), []byte{}, 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	hc := NewTPUHealthChecker(AllocatableDevices{}, nil, dir, "v6e", nil)

	present, err := hc.deviceExists("vfio0")
	if err != nil {
		t.Fatalf("deviceExists(present): %v", err)
	}
	if !present {
		t.Error("expected existing device to report present")
	}

	absent, err := hc.deviceExists("missing")
	if err != nil {
		t.Fatalf("deviceExists(absent): %v", err)
	}
	if absent {
		t.Error("expected missing device to report absent")
	}
}

// stopWithin calls hc.Stop and fails the test if it does not return in time.
func stopWithin(t *testing.T, hc *TPUHealthChecker) {
	t.Helper()
	returned := make(chan struct{})
	go func() {
		hc.Stop()
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return")
	}
}

func TestHealthCheckerStopAfterStart(t *testing.T) {
	hc := NewTPUHealthChecker(AllocatableDevices{}, nil, t.TempDir(), "v6e", nil)
	if err := hc.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	stopWithin(t, hc)
	select {
	case <-hc.done:
	default:
		t.Error("expected the monitor goroutine to have exited after Stop")
	}

	// A second Stop must neither panic nor hang.
	stopWithin(t, hc)
}

func TestHealthCheckerStopWithoutStart(t *testing.T) {
	hc := NewTPUHealthChecker(AllocatableDevices{}, nil, t.TempDir(), "v6e", nil)
	stopWithin(t, hc)
}

func TestHealthCheckerStopAfterMonitorExited(t *testing.T) {
	hc := NewTPUHealthChecker(AllocatableDevices{}, nil, t.TempDir(), "v6e", nil)
	// Simulate a monitor goroutine that already returned on its own.
	hc.started.Store(true)
	close(hc.done)

	stopWithin(t, hc)
}
