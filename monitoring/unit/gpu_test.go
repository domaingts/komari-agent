package monitoring

import (
	"testing"
)

func TestGpuName(t *testing.T) {
	name := GpuName()
	if name == "" || name == "Unknown" {
		t.Errorf("Expected GPU name, got empty or 'Unknown'")
	}
	t.Logf("GPU name: %s", name)
}

func TestFormatGPUNameList(t *testing.T) {
	got := formatGPUNameList([]string{
		"NVIDIA GeForce RTX 4090",
		"NVIDIA GeForce RTX 4090",
		"AMD Radeon RX 7900 XTX",
		"  ",
	})
	want := "NVIDIA GeForce RTX 4090 × 2, AMD Radeon RX 7900 XTX"
	if got != want {
		t.Fatalf("formatGPUNameList() = %q, want %q", got, want)
	}
}

func TestFormatGPUNameListEmpty(t *testing.T) {
	if got := formatGPUNameList(nil); got != "None" {
		t.Fatalf("formatGPUNameList(nil) = %q, want None", got)
	}
}

func TestDetailedGPUEmptyFixtures(t *testing.T) {
	amd := &ROCmSMI{data: []byte(`{}`)}
	amdInfo, err := amd.gatherDetailedInfo()
	if err != nil {
		t.Fatalf("empty AMD fixture failed: %v", err)
	}
	if len(amdInfo) != 0 {
		t.Fatalf("empty AMD fixture returned %d GPUs", len(amdInfo))
	}

	nvidia := &NvidiaSMI{data: []byte(`<nvidia_smi_log></nvidia_smi_log>`)}
	nvidiaInfo, err := nvidia.gatherDetailedInfo()
	if err != nil {
		t.Fatalf("empty NVIDIA fixture failed: %v", err)
	}
	if len(nvidiaInfo) != 0 {
		t.Fatalf("empty NVIDIA fixture returned %d GPUs", len(nvidiaInfo))
	}
}
