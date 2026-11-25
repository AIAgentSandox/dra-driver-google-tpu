/*
 * Copyright The Kubernetes Authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	twist             = "false"
	vlpMaxTopologyDim = 16
)

var (
	acceleratorRegex     = regexp.MustCompile(`^tpu\d+[a-z]?$`)
	pastAcceleratorRegex = regexp.MustCompile(`^tpu-v\d+([ep]?-slice)?((?:-lite)?-(device|podslice))?$`)

	// chips per node -> chips per dimension.
	requestedChipCountToChipsPerDimNumaAligned = map[int][]int64{
		1: {1, 1, 1},
		2: {1, 2, 1},
		4: {2, 2, 1},
		8: {2, 4, 1},
	}

	validSubsliceTopologySet = map[string]bool{
		"1x1":   true,
		"2x2":   true,
		"2x4":   true,
		"4x4":   true,
		"4x8":   true,
		"8x8":   true,
		"8x16":  true,
		"16x16": true,
		"4x4x4": true,
		"2x4x4": true,
		"2x2x4": true,
		"2x2x2": true,
		"2x2x1": true,
	}
)

func IsValidSubSliceTopology(topology string, subSliceTopology string) (bool, error) {
	// subSliceTopology not specified
	if subSliceTopology == "" {
		return false, nil
	}
	// subSliceTopology not valid
	if _, ok := validSubsliceTopologySet[subSliceTopology]; !ok {
		return false, fmt.Errorf("invalid value for subSliceTopology: %s", subSliceTopology)
	}

	// Normalize node topology
	dims, err := getTopologyDims(topology)
	if err != nil {
		return false, fmt.Errorf("invalid node topology %s: %w", topology, err)
	}

	// Normalize subslice topology
	subDims, err := getTopologyDims(subSliceTopology)
	if err != nil {
		return false, fmt.Errorf("invalid value for subSliceTopology %s: %w", subSliceTopology, err)
	}

	for i := range dims {
		d1 := dims[i]    // From control plane (normalized)
		d2 := subDims[i] // From user input (normalized)
		if d2 > d1 {
			return false, fmt.Errorf("invalid value for subSliceTopology: %s, subSliceTopology shouldn't be larger than topology", subSliceTopology)
		}
	}
	return true, nil
}

func ChipCount(chipCount string) (int, error) {
	count, err := strconv.Atoi(chipCount)
	if err != nil {
		return -1, fmt.Errorf("invalid TPU chip count %q", chipCount)
	}
	if count <= 0 {
		return -1, fmt.Errorf("invalid TPU chip count %q: must be positive", chipCount)
	}
	return count, nil
}

// TPU accelerator type from node label should be in format:
// "tpu-<gen>-<device/podslice/slice>".
// We convert it to "<gen>-<# of cores>" for consumption by
// libtpu, frameworks, etc.
func convertAcceleratorType(tpuGen string, topologyDims []int64) (string, error) {
	cores, err := numCores(tpuGen, topologyDims)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%d", tpuGen, cores), nil
}

// AcceleratorGen obtains the generation (v3, v4, v4lite, etc.)
// from the accelerator type label.
// accelerator: tpu-v3-device or tpu-v3-slice; return: v3
// accelerator: tpu-v4-podslice; return: v4
// accelerator: tpu-v4-lite-device; return: v4lite
// accelerator: tpu-v5-lite-device; return: v5lite
// accelerator: tpu-v5-lite-podslice; return: v5litepod
// accelerator: tpu-v5p-slice; return: v5p
// accelerator: tpu-v6e-slice; return: v6e
// reference: https://docs.cloud.google.com/kubernetes-engine/docs/concepts/plan-tpus#standard
//
// The generations it can return, and how to add a new one, are defined by
// tpuGenerationFamilies in hardware.go.
func AcceleratorGen(accelerator string) (string, error) {
	var gen string
	switch {
	case acceleratorRegex.MatchString(accelerator):
		gen = accelerator
	case pastAcceleratorRegex.MatchString(accelerator):
		// Edge cases that match regex but are not valid accelerator types.
		if accelerator == "tpu-v4-device" || accelerator == "tpu-v4-lite-podslice" {
			return "", fmt.Errorf("no such accelerator type: %s", accelerator)
		}

		// gen = v2, v3, v4, v5, v5p, v6e
		gen = strings.Split(accelerator, "-")[1]

		// append 'lite' to lite device and lite podslice
		if strings.Contains(accelerator, "lite") {
			gen = fmt.Sprintf("%slite", gen)
		}

		// append 'pod' to v5 lite podslices
		if strings.HasPrefix(gen, "v5") && strings.Contains(accelerator, "podslice") {
			gen = fmt.Sprintf("%spod", gen)
		}
	default:
		return "", fmt.Errorf("invalid accelerator type: %v", accelerator)
	}

	if !isValidTPUGeneration(gen) {
		return "", fmt.Errorf("invalid TPU generation: %s", gen)
	}
	return gen, nil
}

// numCores calculates the number of cores based on the topology.
// Lite = 1 core per chip
// Non-lite = 2 cores per chip.
func numCores(tpuGen string, topologyDims []int64) (int, error) {
	// Calculate total chips in the podslice.
	totalChips, err := calculateTotalChips(topologyDims)
	if err != nil {
		return 0, err
	}

	// lite-device and lite-podslice have 1 core per chip.
	// v6e is also a "lite"
	if strings.Contains(tpuGen, "lite") || strings.Contains(tpuGen, "v6e") {
		return totalChips, nil
	}
	// device and podslice have 2 cores per chip.
	return totalChips * 2, nil
}

func calculateHostBounds(requestedChipCount int, topologyDims []int64) (string, error) {
	if len(topologyDims) != 3 {
		return "", fmt.Errorf("invalid topology dimensions: expected 3, got %d", len(topologyDims))
	}

	// Get chips per dimension from the chipCount.
	trayChipNumPerDim, exists := requestedChipCountToChipsPerDimNumaAligned[requestedChipCount]
	if !exists {
		return "", fmt.Errorf("invalid value for chipCount: %d", requestedChipCount)
	}

	// Calculate host bounds using topology dimensions and chips per dimension.
	var hostBounds []string
	for dim, trayChipNum := range trayChipNumPerDim {
		hostBounds = append(hostBounds, strconv.FormatInt(topologyDims[dim]/trayChipNum, 10))
	}
	return strings.Join(hostBounds, ","), nil
}

// maxTotalChips bounds the product of topology dimensions so an out-of-range
// topology is rejected instead of silently overflowing calculateTotalChips.
// The largest real TPU topology is 16x16x16 = 4096 chips; the cap is generous.
const maxTotalChips = 1 << 20

// calculateTotalChips multiplies the topology dimensions, guarding against
// overflow at the multiplication itself: it checks before each multiply so an
// oversized dimension cannot wrap int64 to a negative value.
func calculateTotalChips(topologyDims []int64) (int, error) {
	totalChips := int64(1)
	for _, chips := range topologyDims {
		if chips <= 0 {
			return 0, fmt.Errorf("invalid topology dimension %d in %v: must be positive", chips, topologyDims)
		}
		if totalChips > maxTotalChips/chips {
			return 0, fmt.Errorf("topology dimensions %v exceed the maximum of %d chips", topologyDims, maxTotalChips)
		}
		totalChips *= chips
	}
	return int(totalChips), nil
}

func getTopologyDims(topology string) ([]int64, error) {
	var topologyDims []int64
	topologyDimStrs := strings.Split(topology, "x")
	for _, s := range topologyDimStrs {
		n, err := strconv.Atoi(s)
		if err != nil {
			return nil, err
		}
		if n <= 0 {
			return nil, fmt.Errorf("invalid topology dimension %q in %s: must be positive", s, topology)
		}
		topologyDims = append(topologyDims, int64(n))
	}

	// Add 3rd dimension of 1 to 2D topologies (e.g. 2x2 -> 2x2x1).
	if len(topologyDims) == 2 {
		topologyDims = append(topologyDims, int64(1))
	}
	if len(topologyDims) != 3 {
		return nil, fmt.Errorf("invalid topology format: %s, must be 2D or 3D", topology)
	}
	return topologyDims, nil
}

func getChipsPerHostBounds(requestedChipCount int) (string, error) {
	chipsPerDim, exists := requestedChipCountToChipsPerDimNumaAligned[requestedChipCount]
	if !exists {
		return "", fmt.Errorf("invalid chip count: %d", requestedChipCount)
	}
	var tmp []string
	for _, chips := range chipsPerDim {
		tmp = append(tmp, strconv.Itoa(int(chips)))
	}
	return strings.Join(tmp, ","), nil
}

func isSingleHost(chipCount int, topologyDims []int64) (bool, error) {
	// If multiplication of topology dimensions == chip count on this node,
	// this is a single host.
	totalChips, err := calculateTotalChips(topologyDims)
	if err != nil {
		return false, err
	}
	return chipCount == totalChips, nil
}

func wrap(tpuGen string, topologyDims []int64) (string, error) {
	switch tpuGen {
	case "v3", "v4", "v4lite", "v5p":
		return wrapVersion(topologyDims), nil
	case "v5lite", "v5litepod", "v6e":
		return wrapLitePod(topologyDims), nil
	}
	return "", fmt.Errorf("invalid TPU generation: %s", tpuGen)
}

func wrapVersion(topologyDims []int64) string {
	// v4 does wraparound for v4 cube (4x4x4) or larger.
	if cubeOrLarger(topologyDims) {
		return "true,true,true"
	}
	return "false,false,false"
}

func wrapLitePod(topologyDims []int64) string {
	val := []string{"false", "false", "false"}
	for i, dim := range topologyDims {
		if dim == vlpMaxTopologyDim {
			val[i] = "true"
		}
	}
	return strings.Join(val, ",")
}

func isPodslice(accelerator string) bool {
	return strings.HasSuffix(accelerator, "slice")
}

func cubeOrLarger(topologyDims []int64) bool {
	// v4 cube is 4x4x4.
	for _, dim := range topologyDims {
		if dim < 4 {
			return false
		}
	}
	return true
}

// singleHostTopology returns the topology of a single host TPU with the given
// number of chips. Nodes that are part of a multi host slice must get their
// topology from the platform, it cannot be observed locally.
func singleHostTopology(chipCount int) string {
	dims, ok := requestedChipCountToChipsPerDimNumaAligned[chipCount]
	if !ok {
		return ""
	}
	// Trailing dimensions of a single chip are implicit, 2x4x1 is written 2x4.
	for len(dims) > 2 && dims[len(dims)-1] == 1 {
		dims = dims[:len(dims)-1]
	}
	parts := make([]string, 0, len(dims))
	for _, dim := range dims {
		parts = append(parts, strconv.FormatInt(dim, 10))
	}
	return strings.Join(parts, "x")
}
