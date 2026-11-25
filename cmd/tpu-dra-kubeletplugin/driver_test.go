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
	"context"
	"errors"
	"slices"
	"testing"

	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/dynamic-resource-allocation/kubeletplugin"
	"k8s.io/kubernetes/pkg/kubelet/checkpointmanager"
)

func TestHandleError(t *testing.T) {
	// Smoke test: HandleError must not panic on a nil-safe driver.
	d := &driver{}
	d.HandleError(context.Background(), errors.New("boom"), "while testing")
}

// driverWithState assembles a driver backed by a real (temp-dir) checkpoint
// manager and CDI handler, so Prepare/Unprepare exercise their full path
// without a running kubelet plugin helper.
func driverWithState(t *testing.T, chipCount int, deviceNames ...string) *driver {
	t.Helper()
	dir := t.TempDir()

	state := tpuDeviceState(chipCount, deviceNames...)

	cdi, err := NewCDIHandler(&Config{flags: &Flags{cdiRoot: dir}})
	if err != nil {
		t.Fatalf("NewCDIHandler: %v", err)
	}
	state.cdi = cdi

	cpm, err := checkpointmanager.NewCheckpointManager(dir)
	if err != nil {
		t.Fatalf("NewCheckpointManager: %v", err)
	}
	state.checkpointManager = cpm
	if err := cpm.CreateCheckpoint(DriverPluginCheckpointFile, newCheckpoint()); err != nil {
		t.Fatalf("CreateCheckpoint: %v", err)
	}

	return &driver{
		deviceState: state,
		config:      &Config{flags: &Flags{nodeName: "node-a", cdiRoot: dir}},
	}
}

func TestPrepareAndUnprepareResourceClaims(t *testing.T) {
	d := driverWithState(t, 2, "tpu0", "tpu1")
	ctx := context.Background()

	claim := claimWithResults(
		result(DriverName, "tpu-pool", "tpu0", "tpus"),
		result(DriverName, "tpu-pool", "tpu1", "tpus"),
	)

	prep, err := d.PrepareResourceClaims(ctx, []*resourceapi.ResourceClaim{claim})
	if err != nil {
		t.Fatalf("PrepareResourceClaims: %v", err)
	}
	res, ok := prep[claim.UID]
	if !ok {
		t.Fatalf("no prepare result for claim %s", claim.UID)
	}
	if res.Err != nil {
		t.Fatalf("prepare result error: %v", res.Err)
	}
	if len(res.Devices) != 2 {
		t.Errorf("prepared %d devices, want 2", len(res.Devices))
	}

	unprep, err := d.UnprepareResourceClaims(ctx, []kubeletplugin.NamespacedObject{
		{UID: claim.UID},
	})
	if err != nil {
		t.Fatalf("UnprepareResourceClaims: %v", err)
	}
	if unprep[claim.UID] != nil {
		t.Errorf("unprepare error: %v", unprep[claim.UID])
	}
}

func TestPrepareResourceClaimsPartialRequestFails(t *testing.T) {
	d := driverWithState(t, 2, "tpu0", "tpu1")

	// Only one of two chips requested -> per-claim error, no top-level error.
	claim := claimWithResults(result(DriverName, "tpu-pool", "tpu0", "tpus"))

	prep, err := d.PrepareResourceClaims(context.Background(), []*resourceapi.ResourceClaim{claim})
	if err != nil {
		t.Fatalf("PrepareResourceClaims returned top-level error: %v", err)
	}
	if prep[claim.UID].Err == nil {
		t.Error("expected per-claim error for partial TPU request")
	}
}

func TestUnprepareUnknownClaimIsNoop(t *testing.T) {
	d := driverWithState(t, 2, "tpu0", "tpu1")

	unprep, err := d.UnprepareResourceClaims(context.Background(), []kubeletplugin.NamespacedObject{
		{UID: "never-prepared"},
	})
	if err != nil {
		t.Fatalf("UnprepareResourceClaims: %v", err)
	}
	if unprep["never-prepared"] != nil {
		t.Errorf("expected nil error for unknown claim, got %v", unprep["never-prepared"])
	}
}

func TestShutdownNilReceiver(t *testing.T) {
	var d *driver
	if err := d.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown on nil driver: %v", err)
	}
}

func TestBuildSliceSortsDevicesByName(t *testing.T) {
	d := &driver{
		deviceState: &DeviceState{
			allocatable: AllocatableDevices{
				"tpu-3": &AllocatableDevice{name: "tpu-3", index: 3, allocatable: true},
				"tpu-0": &AllocatableDevice{name: "tpu-0", index: 0, allocatable: true},
				"tpu-2": &AllocatableDevice{name: "tpu-2", index: 2, allocatable: false},
				"tpu-1": &AllocatableDevice{name: "tpu-1", index: 1, allocatable: true},
				"tpu-5": &AllocatableDevice{name: "tpu-5", index: 5, allocatable: true},
				"tpu-4": &AllocatableDevice{name: "tpu-4", index: 4, allocatable: true},
			},
		},
	}

	want := []string{"tpu-0", "tpu-1", "tpu-3", "tpu-4", "tpu-5"}
	// Map iteration order varies between ranges, so build the slice several
	// times; every build must come back in the same sorted order.
	for i := range 10 {
		slice := d.buildSlice()
		got := make([]string, 0, len(slice.Devices))
		for _, dev := range slice.Devices {
			got = append(got, dev.Name)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("build %d: device names = %v, want %v", i, got, want)
		}
	}
}
