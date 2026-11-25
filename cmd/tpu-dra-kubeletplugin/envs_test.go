package main

import (
	"reflect"
	"testing"
)

func TestInitEnvs(t *testing.T) {
	tests := []struct {
		name    string
		opts    InitEnvOptions
		nodeIP  string
		want    map[string]string
		wantErr bool
	}{
		{
			name: "single-host v6e slice sets worker and slice envs",
			opts: InitEnvOptions{
				Accelerator:           "tpu-v6e-slice",
				Topology:              "2x2",
				ChipCount:             4,
				AcceleratorCount:      4,
				RequestedChipCount:    4,
				EnableDeviceSpreading: true,
				IsPriviledged:         true,
				VisibleChipIds:        []string{"0", "1", "2", "3"},
				NumaNodeIds:           []string{"0"},
			},
			nodeIP: "10.0.0.1",
			want: map[string]string{
				"TPU_SKIP_MDS_QUERY":          "true",
				"TPU_TOPOLOGY":                "2x2",
				"TPU_ACCELERATOR_TYPE":        "v6e-4",
				"VBAR_CONTROL_SERVICE_URL":    "10.0.0.1:8353",
				"TPU_VISIBLE_CHIPS":           "0,1,2,3",
				"WORKLOAD_NIC_PREFERRED_NUMA": "0",
				"TPU_TOPOLOGY_ALT":            "false",
				"ALT":                         "false",
				"TPU_TOPOLOGY_WRAP":           "false,false,false",
				"WRAP":                        "false,false,false",
				"HOST_BOUNDS":                 "1,1,1",
				"TPU_HOST_BOUNDS":             "1,1,1",
				"CHIPS_PER_HOST_BOUNDS":       "2,2,1",
				"TPU_CHIPS_PER_HOST_BOUNDS":   "2,2,1",
				"TPU_WORKER_ID":               "0",
				"TPU_WORKER_HOSTNAMES":        "localhost",
			},
		},
		{
			name: "multi-host v4 cube enables ICI resiliency and omits single-host worker envs",
			opts: InitEnvOptions{
				Accelerator:         "tpu-v4-podslice",
				Topology:            "4x4x4",
				ChipCount:           4,
				AcceleratorCount:    4,
				RequestedChipCount:  4,
				EnableICIResiliency: "true",
			},
			want: map[string]string{
				"TPU_SKIP_MDS_QUERY":        "true",
				"TPU_TOPOLOGY":              "4x4x4",
				"TPU_ACCELERATOR_TYPE":      "v4-128",
				"ENABLE_ICI_RESILIENCY":     "true",
				"TPU_TOPOLOGY_ALT":          "false",
				"ALT":                       "false",
				"TPU_TOPOLOGY_WRAP":         "true,true,true",
				"WRAP":                      "true,true,true",
				"HOST_BOUNDS":               "2,2,4",
				"TPU_HOST_BOUNDS":           "2,2,4",
				"CHIPS_PER_HOST_BOUNDS":     "2,2,1",
				"TPU_CHIPS_PER_HOST_BOUNDS": "2,2,1",
			},
		},
		{
			name: "invalid accelerator errors",
			opts: InitEnvOptions{
				Accelerator: "invalid-tpu",
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(NodeIPEnv, tt.nodeIP)
			got, err := InitEnvs(tt.opts)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("InitEnvs(%+v) expected error, got nil", tt.opts)
				}
				return
			}
			if err != nil {
				t.Fatalf("InitEnvs error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("InitEnvs = %v, want %v", got, tt.want)
			}
		})
	}
}
