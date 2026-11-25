package main

import (
	"testing"
)

func TestGetTopologyDims(t *testing.T) {
	tests := []struct {
		name     string
		topology string
		want     []int64
		wantErr  bool
	}{
		{
			name:     "valid 3D topology",
			topology: "2x2x4",
			want:     []int64{2, 2, 4},
			wantErr:  false,
		},
		{
			name:     "valid 2D topology (padded to 3D)",
			topology: "2x2",
			want:     []int64{2, 2, 1},
			wantErr:  false,
		},
		{
			name:     "invalid 1D topology",
			topology: "2",
			want:     nil,
			wantErr:  true,
		},
		{
			name:     "invalid topology non-numeric",
			topology: "2xa",
			want:     nil,
			wantErr:  true,
		},
		{
			name:     "zero dimension rejected",
			topology: "2x0x4",
			want:     nil,
			wantErr:  true,
		},
		{
			name:     "negative dimension rejected",
			topology: "2x-2",
			want:     nil,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getTopologyDims(tt.topology)
			if (err != nil) != tt.wantErr {
				t.Errorf("getTopologyDims() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if len(got) != len(tt.want) {
					t.Errorf("getTopologyDims() len got = %v, want %v", len(got), len(tt.want))
					return
				}
				for i := range got {
					if got[i] != tt.want[i] {
						t.Errorf("getTopologyDims() got[%d] = %v, want %v", i, got[i], tt.want[i])
					}
				}
			}
		})
	}
}

func TestCalculateTotalChips(t *testing.T) {
	tests := []struct {
		name    string
		dims    []int64
		want    int
		wantErr bool
	}{
		{name: "normal topology", dims: []int64{2, 2, 4}, want: 16},
		{name: "oversized product rejected", dims: []int64{1024, 1024, 2}, wantErr: true},
		{name: "single oversized dim does not overflow", dims: []int64{2, 9223372036854775807}, wantErr: true},
		{name: "non-positive dim rejected", dims: []int64{2, 0, 4}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := calculateTotalChips(tt.dims)
			if (err != nil) != tt.wantErr {
				t.Errorf("calculateTotalChips() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("calculateTotalChips() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestChipCount(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{name: "valid count", input: "4", want: 4, wantErr: false},
		{name: "non-numeric rejected", input: "x", want: -1, wantErr: true},
		{name: "zero rejected", input: "0", want: -1, wantErr: true},
		{name: "negative rejected", input: "-1", want: -1, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ChipCount(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ChipCount() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ChipCount() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsValidSubSliceTopology(t *testing.T) {
	tests := []struct {
		name             string
		topology         string
		subSliceTopology string
		want             bool
		wantErr          bool
	}{
		{
			name:             "valid matching 3D subslice",
			topology:         "4x4x4",
			subSliceTopology: "2x2x2",
			want:             true,
			wantErr:          false,
		},
		{
			name:             "valid matching 2D subslice",
			topology:         "4x4",
			subSliceTopology: "2x2",
			want:             true,
			wantErr:          false,
		},
		{
			name:             "equivalent 2D topology and 3D subslice",
			topology:         "2x2",
			subSliceTopology: "2x2x1",
			want:             true,
			wantErr:          false,
		},
		{
			name:             "equivalent 3D topology and 2D subslice",
			topology:         "2x2x1",
			subSliceTopology: "2x2",
			want:             true,
			wantErr:          false,
		},
		{
			name:             "subslice topology larger than topology",
			topology:         "2x2x2",
			subSliceTopology: "4x4x4",
			want:             false,
			wantErr:          true,
		},
		{
			name:             "subslice topology larger than topology after normalization",
			topology:         "4x4",
			subSliceTopology: "2x2x2",
			want:             false,
			wantErr:          true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := IsValidSubSliceTopology(tt.topology, tt.subSliceTopology)
			if (err != nil) != tt.wantErr {
				t.Errorf("IsValidSubSliceTopology() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if got != tt.want {
					t.Errorf("IsValidSubSliceTopology() got = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestAcceleratorGen(t *testing.T) {
	tests := []struct {
		name        string
		accelerator string
		want        string
		wantErr     bool
	}{
		{
			name:        "valid v3 device",
			accelerator: "tpu-v3-device",
			want:        "v3",
			wantErr:     false,
		},
		{
			name:        "valid v3 slice",
			accelerator: "tpu-v3-slice",
			want:        "v3",
			wantErr:     false,
		},
		{
			name:        "valid v4 podslice",
			accelerator: "tpu-v4-podslice",
			want:        "v4",
			wantErr:     false,
		},
		{
			name:        "valid v4 lite device",
			accelerator: "tpu-v4-lite-device",
			want:        "v4lite",
			wantErr:     false,
		},
		{
			name:        "valid v5 lite device",
			accelerator: "tpu-v5-lite-device",
			want:        "v5lite",
			wantErr:     false,
		},
		{
			name:        "valid v5 lite podslice",
			accelerator: "tpu-v5-lite-podslice",
			want:        "v5litepod",
			wantErr:     false,
		},
		{
			name:        "valid v5p slice",
			accelerator: "tpu-v5p-slice",
			want:        "v5p",
			wantErr:     false,
		},
		{
			name:        "valid v6e slice",
			accelerator: "tpu-v6e-slice",
			want:        "v6e",
			wantErr:     false,
		},
		{
			name:        "invalid accelerator random",
			accelerator: "invalid-tpu",
			want:        "",
			wantErr:     true,
		},
		{
			// new-style regex matches but generation is unsupported: #32
			name:        "unsupported new-style future generation",
			accelerator: "tpu7x",
			want:        "",
			wantErr:     true,
		},
		{
			// new-style regex matches shape but is garbage: #32
			name:        "unsupported new-style garbage",
			accelerator: "tpu99z",
			want:        "",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := AcceleratorGen(tt.accelerator)
			if (err != nil) != tt.wantErr {
				t.Errorf("AcceleratorGen() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if got != tt.want {
					t.Errorf("AcceleratorGen() got = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestCalculateHostBounds(t *testing.T) {
	tests := []struct {
		name               string
		requestedChipCount int
		topologyDims       []int64
		want               string
		wantErr            bool
	}{
		{
			name:               "valid bounds 1 chip on 2x2x2",
			requestedChipCount: 1,
			topologyDims:       []int64{2, 2, 2},
			want:               "2,2,2", // 2/1, 2/1, 2/1
			wantErr:            false,
		},
		{
			name:               "valid bounds 2 chips on 2x2x2",
			requestedChipCount: 2,
			topologyDims:       []int64{2, 2, 2},
			want:               "2,1,2", // 2/1, 2/2, 2/1
			wantErr:            false,
		},
		{
			name:               "valid bounds 4 chips on 4x4x4",
			requestedChipCount: 4,
			topologyDims:       []int64{4, 4, 4},
			want:               "2,2,4", // 4/2, 4/2, 4/1
			wantErr:            false,
		},
		{
			name:               "valid bounds 8 chips on 8x8x8",
			requestedChipCount: 8,
			topologyDims:       []int64{8, 8, 8},
			want:               "4,2,8", // 8/2, 8/4, 8/1
			wantErr:            false,
		},
		{
			name:               "invalid chip count",
			requestedChipCount: 3,
			topologyDims:       []int64{4, 4, 4},
			want:               "",
			wantErr:            true,
		},
		{
			name:               "invalid 2D topology",
			requestedChipCount: 4,
			topologyDims:       []int64{4, 4},
			want:               "",
			wantErr:            true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := calculateHostBounds(tt.requestedChipCount, tt.topologyDims)
			if (err != nil) != tt.wantErr {
				t.Errorf("calculateHostBounds() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if got != tt.want {
					t.Errorf("calculateHostBounds() got = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestCubeOrLarger(t *testing.T) {
	tests := []struct {
		name string
		dims []int64
		want bool
	}{
		{name: "all dims >= 4", dims: []int64{4, 4, 4}, want: true},
		{name: "larger than cube", dims: []int64{8, 4, 16}, want: true},
		{name: "one dim below 4", dims: []int64{4, 2, 4}, want: false},
		{name: "all dims below 4", dims: []int64{2, 2, 1}, want: false},
		{name: "empty dims is vacuously true", dims: nil, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cubeOrLarger(tt.dims); got != tt.want {
				t.Errorf("cubeOrLarger(%v) = %v, want %v", tt.dims, got, tt.want)
			}
		})
	}
}

func TestWrap(t *testing.T) {
	tests := []struct {
		name    string
		gen     string
		dims    []int64
		want    string
		wantErr bool
	}{
		{name: "v4 cube wraps around", gen: "v4", dims: []int64{4, 4, 4}, want: "true,true,true"},
		{name: "v5p sub-cube does not wrap", gen: "v5p", dims: []int64{2, 2, 1}, want: "false,false,false"},
		{name: "v6e wraps only at the max dim", gen: "v6e", dims: []int64{vlpMaxTopologyDim, 2, vlpMaxTopologyDim}, want: "true,false,true"},
		{name: "v6e below max does not wrap", gen: "v6e", dims: []int64{2, 2, 2}, want: "false,false,false"},
		{name: "unknown generation errors", gen: "v99", dims: []int64{4, 4, 4}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := wrap(tt.gen, tt.dims)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("wrap(%q) expected error, got nil", tt.gen)
				}
				return
			}
			if err != nil {
				t.Fatalf("wrap(%q) error = %v", tt.gen, err)
			}
			if got != tt.want {
				t.Errorf("wrap(%q, %v) = %q, want %q", tt.gen, tt.dims, got, tt.want)
			}
		})
	}
}

func TestConvertAcceleratorType(t *testing.T) {
	tests := []struct {
		name    string
		gen     string
		dims    []int64
		want    string
		wantErr bool
	}{
		{name: "lite gen is 1 core per chip", gen: "v4lite", dims: []int64{2, 2, 1}, want: "v4lite-4"},
		{name: "v6e is treated as lite", gen: "v6e", dims: []int64{2, 2, 1}, want: "v6e-4"},
		{name: "non-lite is 2 cores per chip", gen: "v4", dims: []int64{2, 2, 1}, want: "v4-8"},
		{name: "invalid topology errors", gen: "v4", dims: []int64{0, 2}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := convertAcceleratorType(tt.gen, tt.dims)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("convertAcceleratorType(%q, %v) expected error", tt.gen, tt.dims)
				}
				return
			}
			if err != nil {
				t.Fatalf("convertAcceleratorType error = %v", err)
			}
			if got != tt.want {
				t.Errorf("convertAcceleratorType(%q, %v) = %q, want %q", tt.gen, tt.dims, got, tt.want)
			}
		})
	}
}
