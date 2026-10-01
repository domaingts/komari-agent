//go:build linux

package monitoring

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func makeAMDSysfsFixture(t *testing.T, root, card, driver string, values map[string]string) string {
	t.Helper()
	deviceDir := filepath.Join(root, card, "device")
	if err := os.MkdirAll(deviceDir, 0755); err != nil {
		t.Fatal(err)
	}
	if driver != "" {
		if err := os.Symlink(filepath.Join("..", "..", "drivers", driver), filepath.Join(deviceDir, "driver")); err != nil {
			t.Fatal(err)
		}
	}
	for name, value := range values {
		path := filepath.Join(deviceDir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(value), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return deviceDir
}

func TestAMDSysfsDetailedInfoFixture(t *testing.T) {
	root := t.TempDir()
	makeAMDSysfsFixture(t, root, "card1", "amdgpu", map[string]string{
		"product_name":        " \n",
		"product_description": " AMD Radeon RX 6600 \n",
		"gpu_busy_percent":    "0\n",
		"mem_info_vram_total": "invalid",
	})
	makeAMDSysfsFixture(t, root, "card0", "amdgpu", map[string]string{
		"product_name":             " AMD Radeon RX 7900 XTX\n",
		"gpu_busy_percent":         "75.5\n",
		"mem_info_vram_total":      "17179869184\n",
		"mem_info_vram_used":       "4294967296\n",
		"hwmon/hwmon0/temp1_label": "memory\n",
		"hwmon/hwmon0/temp1_input": "50000\n",
		"hwmon/hwmon0/temp2_label": "junction\n",
		"hwmon/hwmon0/temp2_input": "70500\n",
	})
	for _, card := range []string{"card0-DP-1", "card"} {
		makeAMDSysfsFixture(t, root, card, "amdgpu", map[string]string{"gpu_busy_percent": "100"})
	}
	makeAMDSysfsFixture(t, root, "card2", "i915", map[string]string{"gpu_busy_percent": "40"})
	makeAMDSysfsFixture(t, root, "card3", "", map[string]string{"gpu_busy_percent": "40"})
	makeAMDSysfsFixture(t, root, "card4", "amdgpu", nil)
	makeAMDSysfsFixture(t, root, "card5", "amdgpu", map[string]string{"gpu_busy_percent": "invalid"})

	infos, err := getAMDSysfsDetailedInfoFrom(root, func() string {
		t.Fatal("GPU name fallback was called despite per-card names")
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []DetailedGPUInfo{
		{Name: "AMD Radeon RX 7900 XTX", MemoryTotal: 17179869184, MemoryUsed: 4294967296, Utilization: 75.5, Temperature: 70},
		{Name: "AMD Radeon RX 6600"},
	}
	if !reflect.DeepEqual(infos, want) {
		t.Fatalf("getAMDSysfsDetailedInfoFrom() = %+v, want %+v", infos, want)
	}
}

func TestAMDSysfsNoUsableMetrics(t *testing.T) {
	tests := []struct {
		name   string
		driver string
		values map[string]string
	}{
		{"missing utilization", "amdgpu", nil},
		{"empty utilization", "amdgpu", map[string]string{"gpu_busy_percent": ""}},
		{"malformed utilization", "amdgpu", map[string]string{"gpu_busy_percent": "invalid"}},
		{"NaN utilization", "amdgpu", map[string]string{"gpu_busy_percent": "NaN"}},
		{"infinite utilization", "amdgpu", map[string]string{"gpu_busy_percent": "+Inf"}},
		{"negative utilization", "amdgpu", map[string]string{"gpu_busy_percent": "-1"}},
		{"excess utilization", "amdgpu", map[string]string{"gpu_busy_percent": "101"}},
		{"other driver", "radeon", map[string]string{"gpu_busy_percent": "25"}},
		{"missing driver", "", map[string]string{"gpu_busy_percent": "25"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			makeAMDSysfsFixture(t, root, "card0", tt.driver, tt.values)
			infos, err := getAMDSysfsDetailedInfoFrom(root, func() string {
				t.Fatal("name fallback was called without usable cards")
				return ""
			})
			if err == nil || infos != nil {
				t.Fatalf("got (%+v, %v), want no metrics and an error", infos, err)
			}
		})
	}
	t.Run("no cards", func(t *testing.T) {
		if cards, err := listAMDSysfsCards(t.TempDir()); err == nil || cards != nil {
			t.Fatalf("got (%+v, %v), want no cards and an error", cards, err)
		}
	})
}

func TestAMDSysfsNameFallback(t *testing.T) {
	for _, name := range []string{"Detected AMD model", "None", ""} {
		t.Run("fallback "+name, func(t *testing.T) {
			root := t.TempDir()
			makeAMDSysfsFixture(t, root, "card0", "amdgpu", map[string]string{"gpu_busy_percent": "25"})
			makeAMDSysfsFixture(t, root, "card1", "amdgpu", map[string]string{"gpu_busy_percent": "100", "product_name": "None"})
			calls := 0
			infos, err := getAMDSysfsDetailedInfoFrom(root, func() string {
				calls++
				return name
			})
			if err != nil {
				t.Fatal(err)
			}
			want := name
			if want == "" || want == "None" {
				want = "AMD GPU"
			}
			if len(infos) != 2 || infos[0].Name != want || infos[1].Name != want || calls != 1 {
				t.Fatalf("names = %+v, fallback calls = %d, want two %q names and one fallback", infos, calls, want)
			}
		})
	}
}

func TestAMDSysfsTemperature(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]string
		want   uint64
	}{
		{"edge preference", map[string]string{"hwmon/hwmon0/temp1_label": "memory", "hwmon/hwmon0/temp1_input": "40000", "hwmon/hwmon0/temp2_label": "edge", "hwmon/hwmon0/temp2_input": "65500"}, 65},
		{"junction preference", map[string]string{"hwmon/hwmon0/temp1_label": "memory", "hwmon/hwmon0/temp1_input": "40000", "hwmon/hwmon0/temp2_label": " Junction \n", "hwmon/hwmon0/temp2_input": "72500"}, 72},
		{"other labeled sensor", map[string]string{"hwmon/hwmon0/temp1_label": "memory", "hwmon/hwmon0/temp1_input": "45000"}, 45},
		{"unlabeled sensor", map[string]string{"hwmon/hwmon0/temp1_input": "invalid", "hwmon/hwmon0/temp2_input": "38000"}, 38},
		{"missing preferred input", map[string]string{"hwmon/hwmon0/temp1_label": "junction", "hwmon/hwmon0/temp2_input": "43000"}, 43},
		{"preferred sensor in another hwmon", map[string]string{"hwmon/hwmon0/temp1_label": "memory", "hwmon/hwmon0/temp1_input": "40000", "hwmon/hwmon1/temp1_label": "edge", "hwmon/hwmon1/temp1_input": "78000"}, 78},
		{"malformed inputs", map[string]string{"hwmon/hwmon0/temp1_label": "junction", "hwmon/hwmon0/temp1_input": "invalid", "hwmon/hwmon0/temp2_input": "-1"}, 0},
		{"non-temperature label", map[string]string{"hwmon/hwmon0/fan1_label": "junction", "hwmon/hwmon0/fan1_input": "50000"}, 0},
		{"no sensors", nil, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deviceDir := makeAMDSysfsFixture(t, t.TempDir(), "card0", "amdgpu", tt.values)
			if got := readAMDSysfsTemperature(deviceDir); got != tt.want {
				t.Fatalf("readAMDSysfsTemperature() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestAMDDataPrefersROCmAndFallsBack(t *testing.T) {
	rocmErr := errors.New("ROCm failed")
	sysfsErr := errors.New("sysfs failed")
	tests := []struct {
		name      string
		rocm      []float64
		rocmErr   error
		sysfs     []float64
		sysfsErr  error
		want      []float64
		wantErr   error
		wantSysfs bool
	}{
		{"prefer ROCm", []float64{10}, nil, []float64{20}, sysfsErr, []float64{10}, nil, false},
		{"failed ROCm", nil, rocmErr, []float64{20}, nil, []float64{20}, nil, true},
		{"empty ROCm", nil, nil, []float64{20}, nil, []float64{20}, nil, true},
		{"empty ROCm slice", []float64{}, nil, []float64{20}, nil, []float64{20}, nil, true},
		{"ROCm data with an error", []float64{10}, rocmErr, []float64{20}, nil, []float64{20}, nil, true},
		{"sysfs error", nil, rocmErr, nil, sysfsErr, nil, sysfsErr, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rocmCalls, sysfsCalls := 0, 0
			got, err := getAMDData(func() ([]float64, error) {
				rocmCalls++
				return tt.rocm, tt.rocmErr
			}, func() ([]float64, error) {
				sysfsCalls++
				return tt.sysfs, tt.sysfsErr
			})
			if !reflect.DeepEqual(got, tt.want) || !errors.Is(err, tt.wantErr) {
				t.Fatalf("getAMDData() = (%v, %v), want (%v, %v)", got, err, tt.want, tt.wantErr)
			}
			wantSysfsCalls := 0
			if tt.wantSysfs {
				wantSysfsCalls = 1
			}
			if rocmCalls != 1 || sysfsCalls != wantSysfsCalls {
				t.Fatalf("ROCm/sysfs calls = %d/%d, want 1/%d", rocmCalls, sysfsCalls, wantSysfsCalls)
			}
		})
	}
}
