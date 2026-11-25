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
	"os"
	"strings"

	"k8s.io/klog/v2"
)

const (
	ICIResiliencyEnv = "ENABLE_ICI_RESILIENCY"
	NodeIPEnv        = "NODE_IP"
)

// InitEnvOptions contains fields that are required to initiliazing the
// environment variables used by tpu-device-plugin.
type InitEnvOptions struct {
	Accelerator           string
	Topology              string
	EnableICIResiliency   string
	ChipCount             int
	AcceleratorCount      int
	RequestedChipCount    int
	SubSliceTopology      string
	IsPriviledged         bool
	VisibleChipIds        []string
	EnableDeviceSpreading bool
	NumaNodeIds           []string
}

// InitEnvs initializes a map of environment variables containing required
// metadata values for TPU workloads to run.
func InitEnvs(opts InitEnvOptions) (map[string]string, error) {
	// Get accelerator generation (v4, v5, etc.) and topology dimensions from node labels.
	tpuGen, err := AcceleratorGen(opts.Accelerator)
	if err != nil {
		return nil, err
	}
	var topology string
	valid, err := IsValidSubSliceTopology(opts.Topology, opts.SubSliceTopology)
	if valid {
		topology = opts.SubSliceTopology
	} else {
		topology = opts.Topology
	}
	if err != nil {
		klog.Errorf("Invalid subSliceTopology: %v, setting the topology env as %s.", err, topology)
	}

	var topologyDims []int64
	var acceleratorTypeConverted string
	envs := map[string]string{
		"TPU_SKIP_MDS_QUERY": "true",
	}

	if topology != "" {
		topologyDims, err = getTopologyDims(topology)
		if err != nil {
			return nil, err
		}
		// Convert accelerator type to <tpuGeneration>-<numCores> format used by GKE.
		acceleratorTypeConverted, err = convertAcceleratorType(tpuGen, topologyDims)
		if err != nil {
			return nil, err
		}
		envs["TPU_TOPOLOGY"] = topology
		envs["TPU_ACCELERATOR_TYPE"] = acceleratorTypeConverted
	}

	nodeIp, err := GetEnvName(NodeIPEnv)
	if err != nil {
		klog.Infof("$NODE_IP is not set in env")
	} else {
		envs["VBAR_CONTROL_SERVICE_URL"] = nodeIp + ":8353"
	}

	if opts.EnableDeviceSpreading && opts.IsPriviledged && len(opts.VisibleChipIds) > 0 {
		envs["TPU_VISIBLE_CHIPS"] = strings.Join(opts.VisibleChipIds, ",")
	}

	if len(opts.NumaNodeIds) > 0 {
		envs["WORKLOAD_NIC_PREFERRED_NUMA"] = strings.Join(opts.NumaNodeIds, ",")
	}

	// Set metadata specific to TPU podslices, TPU slice or TPU devices.
	if isPodslice(opts.Accelerator) && topology != "" {
		if err := addPodsliceOrSliceEnvs(tpuGen, opts.EnableICIResiliency, opts.RequestedChipCount, topologyDims, envs); err != nil {
			return nil, err
		}
	}

	// For single host we can add additional env vars to further reduce
	// configuration required by user.
	singleHost, err := isSingleHost(opts.ChipCount, topologyDims)
	if err != nil {
		return nil, err
	}
	if singleHost {
		addSingleHostEnvs(envs)
	}
	return envs, nil
}

func GetEnvName(envName string) (string, error) {
	env := os.Getenv(envName)
	if len(env) == 0 {
		return "", fmt.Errorf("empty %s environment variable", envName)
	}
	return env, nil
}

// Add podslice or slice Envs.
func addPodsliceOrSliceEnvs(tpuGen, enableICIResiliency string, requestedChipCount int, topologyDims []int64, envs map[string]string) error {
	hostBounds, err := calculateHostBounds(requestedChipCount, topologyDims)
	if err != nil {
		return err
	}
	chipsPerHostBounds, err := getChipsPerHostBounds(requestedChipCount)
	if err != nil {
		return err
	}

	wrapVal, err := wrap(tpuGen, topologyDims)
	if err != nil {
		return err
	}

	// Enable ICI resiliency on for v4 / v5p topologies >= 4x4x4.
	if strings.HasPrefix(tpuGen, "v4") || strings.HasPrefix(tpuGen, "v5p") {
		if cubeOrLarger(topologyDims) {
			envs[ICIResiliencyEnv] = "true"
			if strings.ToLower(enableICIResiliency) == "false" {
				envs[ICIResiliencyEnv] = "false"
			}
		}
	}

	envs["TPU_TOPOLOGY_ALT"] = twist
	envs["ALT"] = twist
	envs["TPU_TOPOLOGY_WRAP"] = wrapVal
	envs["WRAP"] = wrapVal
	envs["HOST_BOUNDS"] = hostBounds
	envs["TPU_HOST_BOUNDS"] = hostBounds
	envs["CHIPS_PER_HOST_BOUNDS"] = chipsPerHostBounds
	envs["TPU_CHIPS_PER_HOST_BOUNDS"] = chipsPerHostBounds
	return nil
}

func addSingleHostEnvs(envs map[string]string) {
	envs["TPU_WORKER_ID"] = "0"
	envs["TPU_WORKER_HOSTNAMES"] = "localhost"
}
