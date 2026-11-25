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
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/compute/metadata"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
)

const (
	// Canonical, vendor neutral node labels the driver understands. Any
	// platform can set these directly, no cloud provider integration is
	// required.
	AcceleratorLabel      = DriverName + "/accelerator"
	AcceleratorCountLabel = DriverName + "/chip-count"
	TopologyLabel         = DriverName + "/topology"
	ICIResiliency         = DriverName + "/ici-resiliency"

	// GKE advertises the same information under its own label keys.
	gkeAcceleratorLabel      = "cloud.google.com/gke-tpu-accelerator"
	gkeAcceleratorCountLabel = "cloud.google.com/gke-accelerator-count"
	gkeTopologyLabel         = "cloud.google.com/gke-tpu-topology"
	gkeICIResiliencyLabel    = "cloud.google.com/gke-tpu-ici-resiliency"

	KubeLabelsPath = "instance/attributes/kube-labels"
	TpuEnvPath     = "instance/attributes/tpu-env"
)

// errNoTPUDetected is returned when the node the driver runs on does not expose
// any TPU hardware. It is not a fatal condition: the driver is expected to be
// deployed on heterogeneous clusters where only a subset of nodes has TPUs.
var errNoTPUDetected = errors.New("no TPU hardware detected on this node")

var (
	// canonicalLabelAliases maps every canonical TPU label to the equivalent
	// keys used by known platforms. Add an entry here to support a new
	// platform without touching the rest of the driver.
	canonicalLabelAliases = map[string][]string{
		AcceleratorLabel:      {gkeAcceleratorLabel},
		AcceleratorCountLabel: {gkeAcceleratorCountLabel},
		TopologyLabel:         {gkeTopologyLabel},
		ICIResiliency:         {gkeICIResiliencyLabel},
	}
)

// parseKubeLabels parses the comma-separated kube-labels format.
func parseKubeLabels(raw string) map[string]string {
	labels := make(map[string]string)
	pairs := strings.Split(raw, ",")
	for _, pair := range pairs {
		parts := strings.Split(pair, "=")
		if len(parts) == 2 {
			labels[parts[0]] = parts[1]
		}
	}
	return labels
}

// parseTpuEnv parses the multi-line tpu-env key-value format.
func parseTpuEnv(raw string) map[string]string {
	envMap := make(map[string]string)
	lines := strings.Split(raw, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			value = strings.Trim(value, "'\"")
			envMap[key] = value
		}
	}
	return envMap
}

// mapTpuEnvToLabels maps parsed tpu-env variables to the canonical TPU labels.
func mapTpuEnvToLabels(envMap map[string]string) map[string]string {
	accelType := envMap["ACCELERATOR_TYPE"]
	if accelType == "" {
		accelType = envMap["TYPE"]
	}
	if accelType == "" {
		return nil
	}

	// Map accelerator to the "tpu-<gen>-<form factor>" naming used by the labels.
	var accelerator string
	accelTypeLower := strings.ToLower(accelType)
	switch {
	case strings.HasPrefix(accelTypeLower, "v5litepod"):
		accelerator = "tpu-v5-lite-podslice"
	case strings.HasPrefix(accelTypeLower, "v5lite"):
		accelerator = "tpu-v5-lite-device"
	case strings.HasPrefix(accelTypeLower, "v5p"):
		accelerator = "tpu-v5p-slice"
	case strings.HasPrefix(accelTypeLower, "v6e"):
		accelerator = "tpu-v6e-slice"
	case strings.HasPrefix(accelTypeLower, "v4"):
		accelerator = "tpu-v4-podslice"
	case strings.HasPrefix(accelTypeLower, "v3"):
		accelerator = "tpu-v3-device"
	default:
		accelerator = accelTypeLower // fallback
	}

	// Determine chip count from CHIPS_PER_HOST_BOUNDS product
	chipCount := "4" // default fallback
	if chipsBounds, exists := envMap["CHIPS_PER_HOST_BOUNDS"]; exists {
		parts := strings.Split(chipsBounds, ",")
		product := 1
		for _, part := range parts {
			val, err := strconv.Atoi(strings.TrimSpace(part))
			if err == nil {
				product *= val
			}
		}
		chipCount = strconv.Itoa(product)
	}

	// Determine topology
	topology := envMap["TOPOLOGY"]
	if topology == "" {
		topology = "2x2" // default fallback
	}

	return map[string]string{
		AcceleratorLabel:      accelerator,
		AcceleratorCountLabel: chipCount,
		TopologyLabel:         topology,
		ICIResiliency:         envMap["ENABLE_ICI_RESILIENCY"],
	}
}

// getNodeLabelsFromMetadata queries GCE MDS for `kube-labels` or falls back to `tpu-env` using the official library.
func getNodeLabelsFromMetadata(ctx context.Context) (map[string]string, error) {
	if !metadata.OnGCE() {
		return nil, fmt.Errorf("not running on a GCE instance")
	}

	var labels map[string]string

	err := wait.PollUntilContextTimeout(ctx, 1*time.Second, 15*time.Second, true, func(ctx context.Context) (done bool, err error) {
		// Try to query kube-labels
		kubeLabelsRaw, err := metadata.GetWithContext(ctx, KubeLabelsPath)
		if err == nil {
			parsed := normalizeTPULabels(parseKubeLabels(kubeLabelsRaw))
			if parsed[AcceleratorLabel] != "" {
				labels = parsed
				return true, nil
			}
		}

		// Try to query tpu-env
		tpuEnvRaw, err := metadata.GetWithContext(ctx, TpuEnvPath)
		if err == nil {
			envMap := parseTpuEnv(tpuEnvRaw)
			mapped := mapTpuEnvToLabels(envMap)
			if mapped != nil && mapped[AcceleratorLabel] != "" {
				labels = mapped
				return true, nil
			}
		}

		klog.Infof("could not get kube-labels or tpu-env on GCE ... retrying")
		return false, nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to get node labels from metadata server: %w", err)
	}

	return labels, nil
}

// normalizeTPULabels translates the TPU labels of any known platform to the
// canonical keys used internally by the driver. Labels unrelated to TPUs and
// keys without a value are dropped.
func normalizeTPULabels(labels map[string]string) map[string]string {
	normalized := make(map[string]string, len(canonicalLabelAliases))
	for canonical, aliases := range canonicalLabelAliases {
		keys := append([]string{canonical}, aliases...)
		for _, key := range keys {
			if value := labels[key]; value != "" {
				normalized[canonical] = value
				break
			}
		}
	}
	return normalized
}

// labelsFromConfig builds the canonical TPU labels from the driver command line
// flags. It allows running on hardware the driver cannot introspect on its own.
func labelsFromConfig(f *Flags) (map[string]string, error) {
	labels := normalizeTPULabels(map[string]string{
		AcceleratorLabel:      f.tpuAccelerator,
		AcceleratorCountLabel: f.tpuChipCount,
		TopologyLabel:         f.tpuTopology,
		ICIResiliency:         f.tpuICIResiliency,
	})
	if labels[AcceleratorLabel] == "" {
		return nil, fmt.Errorf("TPU accelerator not configured")
	}
	return labels, nil
}

// labelsFromTPUEnvFile derives the canonical TPU labels from the tpu-env file
// written by the Cloud TPU runtime on the host, e.g. /etc/tpu/tpu-env.
func labelsFromTPUEnvFile(path string) (map[string]string, error) {
	if path == "" {
		return nil, fmt.Errorf("no tpu-env file configured")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	labels := mapTpuEnvToLabels(parseTpuEnv(string(raw)))
	if labels[AcceleratorLabel] == "" {
		return nil, fmt.Errorf("%s does not describe a TPU", path)
	}
	return labels, nil
}

// labelsFromNode derives the canonical TPU labels from the labels the platform
// set on the Node object. The driver only reads them, what it discovers is
// published on the devices of the ResourceSlice.
func labelsFromNode(ctx context.Context, config *Config) (map[string]string, error) {
	if config.coreclient == nil || config.flags.nodeName == "" {
		return nil, fmt.Errorf("no Kubernetes client available")
	}
	node, err := config.coreclient.CoreV1().Nodes().Get(ctx, config.flags.nodeName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return normalizeTPULabels(node.Labels), nil
}

type tpuLabelSource struct {
	name string
	get  func(context.Context) (map[string]string, error)
}

func defaultTPULabelSources(config *Config) []tpuLabelSource {
	return []tpuLabelSource{
		{"driver configuration", func(context.Context) (map[string]string, error) {
			return labelsFromConfig(config.flags)
		}},
		{fmt.Sprintf("tpu-env file %q", config.flags.tpuEnvFilePath), func(context.Context) (map[string]string, error) {
			return labelsFromTPUEnvFile(config.flags.tpuEnvFilePath)
		}},
		{"GCE metadata server", getNodeLabelsFromMetadata},
		{"Kubernetes node object", func(ctx context.Context) (map[string]string, error) {
			return labelsFromNode(ctx, config)
		}},
	}
}

// getTPUNodeLabels describes the TPU of the local node with the canonical
// labels. Sources are tried in order of decreasing precedence so that an
// explicit configuration always wins over auto discovery, and whatever is left
// unknown is completed from hardware, which the caller has already probed so
// that the fail-fast check on a node without a TPU happens once, right at the
// entry point of NewDriver (driver.go).
func getTPUNodeLabels(ctx context.Context, config *Config, hardware *tpuHardware) (map[string]string, error) {
	return getTPUNodeLabelsFromSources(ctx, defaultTPULabelSources(config), hardware)
}

func getTPUNodeLabelsFromSources(ctx context.Context, sources []tpuLabelSource, hardware *tpuHardware) (map[string]string, error) {
	labels := map[string]string{}
	for _, source := range sources {
		found, err := source.get(ctx)
		if err != nil {
			klog.V(3).Infof("No TPU information from %s: %v", source.name, err)
			continue
		}
		if found[AcceleratorLabel] == "" {
			klog.V(3).Infof("No TPU information from %s", source.name)
			continue
		}
		klog.Infof("Discovered TPU %v from %s", found, source.name)
		labels = found
		break
	}

	completeLabelsFromHardware(labels, hardware)
	if labels[AcceleratorLabel] == "" {
		hint := ""
		if hardware.generation != "" {
			hint = fmt.Sprintf(" of generation %s (read from their PCI id)", hardware.generation)
		}
		return nil, fmt.Errorf("found %d TPU chips%s in %s but no source knows their type, set --tpu-accelerator or the %s node label",
			hardware.chipCount, hint, hardware.devDirectory, AcceleratorLabel)
	}
	return labels, nil
}

// completeLabelsFromHardware fills in the TPU properties that no source knew
// about with the ones observed on the host.
func completeLabelsFromHardware(labels map[string]string, hardware *tpuHardware) {
	chipCount := strconv.Itoa(hardware.chipCount)
	switch labels[AcceleratorCountLabel] {
	case "":
		labels[AcceleratorCountLabel] = chipCount
	case chipCount:
	default:
		klog.Warningf("Node is labeled with %s TPU chips but %s holds %s devices", labels[AcceleratorCountLabel], hardware.devDirectory, chipCount)
	}

	// Cross-check against the generation read from the PCI id (see
	// tpuGenerationFromPCIIDs in hardware.go), when the hardware could tell it.
	if hardware.generation != "" && labels[AcceleratorLabel] != "" {
		if gen, err := AcceleratorGen(labels[AcceleratorLabel]); err == nil && gen != hardware.generation {
			klog.Warningf("Node is labeled with accelerator %q (generation %s) but its PCI id says generation %s", labels[AcceleratorLabel], gen, hardware.generation)
		}
	}

	if labels[TopologyLabel] == "" {
		if topology := singleHostTopology(hardware.chipCount); topology != "" {
			klog.Infof("Assuming the single host topology %s for %d TPU chips", topology, hardware.chipCount)
			labels[TopologyLabel] = topology
		}
	}
}

// isSingleHostNode reports whether the TPU chips on this node make up an entire
// slice, the only topology where sharing is supported.
//
// Non-podslice devices are single-host by definition. For podslices, a node is
// single-host when its chip count equals the product of the slice topology dimensions.
// If topology or chip count is missing or unparseable, the node is treated as
// multi-host to safely prevent sharing chips that belong to a larger slice.
func isSingleHostNode(nodeLabels map[string]string) bool {
	if !isPodslice(nodeLabels[AcceleratorLabel]) {
		return true
	}

	topology := nodeLabels[TopologyLabel]
	if topology == "" {
		klog.Warningf("node label %s is not set and no single-host topology could be inferred "+
			"from the hardware; treating the node as multi-host to prevent unsafe sharing across slice boundaries", TopologyLabel)
		return false
	}
	topologyDims, err := getTopologyDims(topology)
	if err != nil {
		klog.Warningf("cannot parse node label %s=%q: %v; treating the node as multi-host to prevent unsafe sharing across slice boundaries", TopologyLabel, topology, err)
		return false
	}

	chipCount, err := ChipCount(nodeLabels[AcceleratorCountLabel])
	if err != nil {
		klog.Warningf("cannot parse node label %s=%q: %v; treating the node as multi-host to prevent unsafe sharing across slice boundaries",
			AcceleratorCountLabel, nodeLabels[AcceleratorCountLabel], err)
		return false
	}

	singleHost, err := isSingleHost(chipCount, topologyDims)
	if err != nil {
		klog.Warningf("cannot determine whether node is single-host: %v; treating the node as multi-host to prevent unsafe sharing across slice boundaries", err)
		return false
	}
	return singleHost
}
