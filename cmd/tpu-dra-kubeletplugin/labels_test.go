package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestGetNodeLabelsFromMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/computeMetadata/v1/instance/attributes/kube-labels" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("Metadata-Flavor") != "Google" {
			t.Errorf("missing Metadata-Flavor header")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("cloud.google.com/gke-tpu-accelerator=tpu-v5-lite-device,cloud.google.com/gke-accelerator-count=4,cloud.google.com/gke-tpu-topology=2x2"))
	}))
	defer server.Close()

	t.Setenv("GCE_METADATA_HOST", server.Listener.Addr().String())

	got, err := getNodeLabelsFromMetadata(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := map[string]string{
		"tpu.google.com/accelerator": "tpu-v5-lite-device",
		"tpu.google.com/chip-count":  "4",
		"tpu.google.com/topology":    "2x2",
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("getNodeLabelsFromMetadata() got = %v, want %v", got, want)
	}
}

func TestGetNodeLabelsFromMetadata_TPUEnvFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Metadata-Flavor") != "Google" {
			t.Errorf("missing Metadata-Flavor header")
		}

		if r.URL.Path == "/computeMetadata/v1/instance/attributes/kube-labels" {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		if r.URL.Path == "/computeMetadata/v1/instance/attributes/tpu-env" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`ACCELERATOR_TYPE: 'v5litepod-8'
CHIPS_PER_HOST_BOUNDS: '2,4,1'
ENABLE_ICI_RESILIENCY: 'false'
TOPOLOGY: '2x4'
`))
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	t.Setenv("GCE_METADATA_HOST", server.Listener.Addr().String())

	got, err := getNodeLabelsFromMetadata(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := map[string]string{
		"tpu.google.com/accelerator":    "tpu-v5-lite-podslice",
		"tpu.google.com/chip-count":     "8",
		"tpu.google.com/topology":       "2x4",
		"tpu.google.com/ici-resiliency": "false",
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("getNodeLabelsFromMetadata() got = %v, want %v", got, want)
	}
}

func TestNormalizeTPULabels(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
		want   map[string]string
	}{
		{
			name: "gke labels",
			labels: map[string]string{
				"cloud.google.com/gke-tpu-accelerator":   "tpu-v6e-slice",
				"cloud.google.com/gke-accelerator-count": "8",
				"cloud.google.com/gke-tpu-topology":      "2x4",
				"kubernetes.io/os":                       "linux",
			},
			want: map[string]string{
				"tpu.google.com/accelerator": "tpu-v6e-slice",
				"tpu.google.com/chip-count":  "8",
				"tpu.google.com/topology":    "2x4",
			},
		},
		{
			name: "canonical labels take precedence",
			labels: map[string]string{
				"tpu.google.com/accelerator":           "tpu-v6e-slice",
				"cloud.google.com/gke-tpu-accelerator": "tpu-v4-podslice",
			},
			want: map[string]string{
				"tpu.google.com/accelerator": "tpu-v6e-slice",
			},
		},
		{
			name:   "no tpu labels",
			labels: map[string]string{"kubernetes.io/os": "linux"},
			want:   map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeTPULabels(tt.labels); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("normalizeTPULabels() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsSingleHostNode(t *testing.T) {
	for _, tc := range []struct {
		name   string
		labels map[string]string
		want   bool
	}{
		{
			name: "single-host podslice: 4 chips, 2x2x1 topology",
			labels: map[string]string{
				AcceleratorLabel:      "tpu-v4-podslice",
				TopologyLabel:         "2x2x1",
				AcceleratorCountLabel: "4",
			},
			want: true,
		},
		{
			name: "single-host podslice: 8 chips, 2x4 topology",
			labels: map[string]string{
				AcceleratorLabel:      "tpu-v5-lite-podslice",
				TopologyLabel:         "2x4",
				AcceleratorCountLabel: "8",
			},
			want: true,
		},
		{
			name: "multi-host podslice: 4 chips of a 4x4x4 slice",
			labels: map[string]string{
				AcceleratorLabel:      "tpu-v4-podslice",
				TopologyLabel:         "4x4x4",
				AcceleratorCountLabel: "4",
			},
			want: false,
		},
		{
			name: "non-slice accelerators are single-host by definition",
			labels: map[string]string{
				AcceleratorLabel:      "tpu-v5-lite-device",
				AcceleratorCountLabel: "1",
			},
			want: true,
		},
		{
			// getTPUNodeLabels normally fills this in via completeLabelsFromHardware,
			// so reaching here means neither the platform nor the hardware could
			// supply a topology.
			name: "podslice with no topology label is treated as multi-host",
			labels: map[string]string{
				AcceleratorLabel:      "tpu-v4-podslice",
				AcceleratorCountLabel: "4",
			},
			want: false,
		},
		{
			// completeLabelsFromHardware assumes a single-host topology for a chip
			// count it recognizes, trimming trailing unit dimensions (4 chips ->
			// "2x2"). Such a node must come out single-host.
			name: "topology inferred from hardware by completeLabelsFromHardware",
			labels: func() map[string]string {
				labels := map[string]string{AcceleratorLabel: "tpu-v4-podslice"}
				completeLabelsFromHardware(labels, &tpuHardware{chipCount: 4})
				return labels
			}(),
			want: true,
		},
		{
			name: "unparsable topology is treated as multi-host",
			labels: map[string]string{
				AcceleratorLabel:      "tpu-v4-podslice",
				TopologyLabel:         "not-a-topology",
				AcceleratorCountLabel: "4",
			},
			want: false,
		},
		{
			name: "unparsable chip count is treated as multi-host",
			labels: map[string]string{
				AcceleratorLabel:      "tpu-v4-podslice",
				TopologyLabel:         "2x2x1",
				AcceleratorCountLabel: "",
			},
			want: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSingleHostNode(tc.labels); got != tc.want {
				t.Errorf("isSingleHostNode(%v) = %v, want %v", tc.labels, got, tc.want)
			}
		})
	}
}

func TestGetTPUNodeLabelsFromSources(t *testing.T) {
	tests := []struct {
		name     string
		sources  []tpuLabelSource
		hardware *tpuHardware
		want     map[string]string
		wantErr  bool
	}{
		{
			name: "first source supplies all labels",
			sources: []tpuLabelSource{
				{
					name: "mock source",
					get: func(context.Context) (map[string]string, error) {
						return map[string]string{
							AcceleratorLabel:      "tpu-v6e-slice",
							AcceleratorCountLabel: "4",
							TopologyLabel:         "2x2",
							ICIResiliency:         "true",
						}, nil
					},
				},
			},
			hardware: &tpuHardware{devDirectory: "/dev", chipCount: 4},
			want: map[string]string{
				AcceleratorLabel:      "tpu-v6e-slice",
				AcceleratorCountLabel: "4",
				TopologyLabel:         "2x2",
				ICIResiliency:         "true",
			},
		},
		{
			name: "first source has error, second source succeeds",
			sources: []tpuLabelSource{
				{
					name: "failing source",
					get: func(context.Context) (map[string]string, error) {
						return nil, errors.New("source unavailable")
					},
				},
				{
					name: "successful source",
					get: func(context.Context) (map[string]string, error) {
						return map[string]string{
							AcceleratorLabel:      "tpu-v6e-slice",
							AcceleratorCountLabel: "4",
							TopologyLabel:         "2x2",
						}, nil
					},
				},
			},
			hardware: &tpuHardware{devDirectory: "/dev", chipCount: 4},
			want: map[string]string{
				AcceleratorLabel:      "tpu-v6e-slice",
				AcceleratorCountLabel: "4",
				TopologyLabel:         "2x2",
			},
		},
		{
			name: "source without accelerator is skipped",
			sources: []tpuLabelSource{
				{
					name: "source without accelerator",
					get: func(context.Context) (map[string]string, error) {
						return map[string]string{
							TopologyLabel: "2x2",
						}, nil
					},
				},
				{
					name: "fallback source",
					get: func(context.Context) (map[string]string, error) {
						return map[string]string{
							AcceleratorLabel:      "tpu-v6e-slice",
							AcceleratorCountLabel: "4",
							TopologyLabel:         "2x2",
						}, nil
					},
				},
			},
			hardware: &tpuHardware{devDirectory: "/dev", chipCount: 4},
			want: map[string]string{
				AcceleratorLabel:      "tpu-v6e-slice",
				AcceleratorCountLabel: "4",
				TopologyLabel:         "2x2",
			},
		},
		{
			name: "hardware fills chip count and single-host topology",
			sources: []tpuLabelSource{
				{
					name: "accelerator only",
					get: func(context.Context) (map[string]string, error) {
						return map[string]string{
							AcceleratorLabel: "tpu-v6e-slice",
						}, nil
					},
				},
			},
			hardware: &tpuHardware{devDirectory: "/dev", chipCount: 4},
			want: map[string]string{
				AcceleratorLabel:      "tpu-v6e-slice",
				AcceleratorCountLabel: "4",
				TopologyLabel:         "2x2",
			},
		},
		{
			name: "no source knows the accelerator",
			sources: []tpuLabelSource{
				{
					name: "empty source",
					get: func(context.Context) (map[string]string, error) {
						return nil, errors.New("not found")
					},
				},
			},
			hardware: &tpuHardware{devDirectory: "/dev", chipCount: 4},
			wantErr:  true,
		},
		{
			name:     "empty sources list",
			sources:  nil,
			hardware: &tpuHardware{devDirectory: "/dev", chipCount: 4},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			labels, err := getTPUNodeLabelsFromSources(context.Background(), tt.sources, tt.hardware)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("getTPUNodeLabelsFromSources: %v", err)
			}
			if !reflect.DeepEqual(labels, tt.want) {
				t.Errorf("labels = %v, want %v", labels, tt.want)
			}
		})
	}
}

func TestDefaultTPULabelSources(t *testing.T) {
	config := &Config{
		flags: &Flags{
			tpuAccelerator: "tpu-v6e-slice",
			tpuChipCount:   "4",
		},
	}
	sources := defaultTPULabelSources(config)
	if len(sources) != 4 {
		t.Fatalf("defaultTPULabelSources returned %d sources, want 4", len(sources))
	}

	// First source is driver configuration and should resolve with the given flags.
	got, err := sources[0].get(context.Background())
	if err != nil {
		t.Fatalf("driver configuration source error: %v", err)
	}
	if got[AcceleratorLabel] != "tpu-v6e-slice" {
		t.Errorf("AcceleratorLabel = %q, want %q", got[AcceleratorLabel], "tpu-v6e-slice")
	}
}

func TestGetTPUNodeLabelsWithConfig(t *testing.T) {
	config := &Config{
		flags: &Flags{
			tpuAccelerator: "tpu-v6e-slice",
			tpuChipCount:   "4",
			tpuTopology:    "2x2",
		},
	}
	hardware := &tpuHardware{devDirectory: "/dev", chipCount: 4}
	labels, err := getTPUNodeLabels(context.Background(), config, hardware)
	if err != nil {
		t.Fatalf("getTPUNodeLabels() unexpected error: %v", err)
	}
	if labels[AcceleratorLabel] != "tpu-v6e-slice" {
		t.Errorf("AcceleratorLabel = %q, want %q", labels[AcceleratorLabel], "tpu-v6e-slice")
	}
}

func TestLabelsFromNodeNoClient(t *testing.T) {
	// Without a client or node name this must fail closed rather than panic.
	if _, err := labelsFromNode(context.Background(), &Config{flags: &Flags{}}); err == nil {
		t.Error("expected error when no Kubernetes client is available")
	}
}

func TestLabelsFromConfig(t *testing.T) {
	tests := []struct {
		name    string
		flags   *Flags
		want    map[string]string
		wantErr bool
	}{
		{
			name: "configured flags return canonical labels",
			flags: &Flags{
				tpuAccelerator:   "tpu-v6e-slice",
				tpuChipCount:     "4",
				tpuTopology:      "2x2",
				tpuICIResiliency: "true",
			},
			want: map[string]string{
				AcceleratorLabel:      "tpu-v6e-slice",
				AcceleratorCountLabel: "4",
				TopologyLabel:         "2x2",
				ICIResiliency:         "true",
			},
		},
		{
			name:    "missing accelerator errors",
			flags:   &Flags{},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := labelsFromConfig(tt.flags)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("labelsFromConfig(%+v) expected error, got nil", tt.flags)
				}
				return
			}
			if err != nil {
				t.Fatalf("labelsFromConfig error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("labelsFromConfig = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMapTpuEnvToLabels(t *testing.T) {
	tests := []struct {
		name   string
		envMap map[string]string
		want   map[string]string
	}{
		{
			name: "v6e with explicit bounds and topology",
			envMap: map[string]string{
				"ACCELERATOR_TYPE":      "v6e-16",
				"CHIPS_PER_HOST_BOUNDS": "2,2,1",
				"TOPOLOGY":              "4x4",
				"ENABLE_ICI_RESILIENCY": "true",
			},
			want: map[string]string{
				AcceleratorLabel:      "tpu-v6e-slice",
				AcceleratorCountLabel: "4",
				TopologyLabel:         "4x4",
				ICIResiliency:         "true",
			},
		},
		{
			name:   "TYPE fallback and default chip/topology",
			envMap: map[string]string{"TYPE": "v5litepod-8"},
			want: map[string]string{
				AcceleratorLabel:      "tpu-v5-lite-podslice",
				AcceleratorCountLabel: "4",
				TopologyLabel:         "2x2",
				ICIResiliency:         "",
			},
		},
		{
			name:   "v5lite non-pod device",
			envMap: map[string]string{"ACCELERATOR_TYPE": "v5lite-8"},
			want: map[string]string{
				AcceleratorLabel:      "tpu-v5-lite-device",
				AcceleratorCountLabel: "4",
				TopologyLabel:         "2x2",
				ICIResiliency:         "",
			},
		},
		{
			name:   "v5p slice",
			envMap: map[string]string{"ACCELERATOR_TYPE": "v5p-16"},
			want: map[string]string{
				AcceleratorLabel:      "tpu-v5p-slice",
				AcceleratorCountLabel: "4",
				TopologyLabel:         "2x2",
				ICIResiliency:         "",
			},
		},
		{
			name:   "v4 podslice",
			envMap: map[string]string{"ACCELERATOR_TYPE": "v4-8"},
			want: map[string]string{
				AcceleratorLabel:      "tpu-v4-podslice",
				AcceleratorCountLabel: "4",
				TopologyLabel:         "2x2",
				ICIResiliency:         "",
			},
		},
		{
			name:   "v3 device",
			envMap: map[string]string{"ACCELERATOR_TYPE": "v3-8"},
			want: map[string]string{
				AcceleratorLabel:      "tpu-v3-device",
				AcceleratorCountLabel: "4",
				TopologyLabel:         "2x2",
				ICIResiliency:         "",
			},
		},
		{
			name:   "unknown type falls back to lowercased value",
			envMap: map[string]string{"ACCELERATOR_TYPE": "V9-Custom"},
			want: map[string]string{
				AcceleratorLabel:      "v9-custom",
				AcceleratorCountLabel: "4",
				TopologyLabel:         "2x2",
				ICIResiliency:         "",
			},
		},
		{
			name:   "no accelerator type returns nil",
			envMap: map[string]string{"TOPOLOGY": "2x2"},
			want:   nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapTpuEnvToLabels(tt.envMap)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("mapTpuEnvToLabels = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLabelsFromTPUEnvFile(t *testing.T) {
	tests := []struct {
		name        string
		emptyPath   bool
		missingFile bool
		content     string
		want        map[string]string
		wantErr     bool
	}{
		{name: "empty path fails closed", emptyPath: true, wantErr: true},
		{name: "missing file surfaces read error", missingFile: true, wantErr: true},
		{name: "non-TPU file is rejected", content: "FOO: bar\n", wantErr: true},
		{
			name:    "well-formed tpu-env yields canonical labels",
			content: "ACCELERATOR_TYPE: 'v6e-16'\nTOPOLOGY: '4x4'\nCHIPS_PER_HOST_BOUNDS: '2,2,1'\n",
			want: map[string]string{
				AcceleratorLabel:      "tpu-v6e-slice",
				AcceleratorCountLabel: "4",
				TopologyLabel:         "4x4",
				ICIResiliency:         "",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var path string
			switch {
			case tt.emptyPath:
				path = ""
			case tt.missingFile:
				path = filepath.Join(t.TempDir(), "missing")
			default:
				path = filepath.Join(t.TempDir(), "tpu-env")
				if err := os.WriteFile(path, []byte(tt.content), 0600); err != nil {
					t.Fatalf("WriteFile: %v", err)
				}
			}

			labels, err := labelsFromTPUEnvFile(path)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("labelsFromTPUEnvFile(%q) expected error, got nil", path)
				}
				return
			}
			if err != nil {
				t.Fatalf("labelsFromTPUEnvFile error = %v", err)
			}
			if !reflect.DeepEqual(labels, tt.want) {
				t.Errorf("labelsFromTPUEnvFile = %v, want %v", labels, tt.want)
			}
		})
	}
}
